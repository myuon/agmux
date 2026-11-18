// Package experimental manages opt-in feature flags for features whose value
// is not yet proven. Flags live in the [experimental] section of config.toml
// and default to off. See CLAUDE.md "Experimental 機能ルール".
package experimental

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/config"
)

// Feature describes a known experimental feature.
type Feature struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Features is the registry of known experimental features. Add an entry here
// when introducing a new experimental feature.
var Features = []Feature{
	{
		Name:        "session_search",
		Title:       "セッション横断検索",
		Description: "全セッションの会話を横断して検索できるようにします。セッション一覧に検索ボックスが出て、エージェントは search_sessions ツールで過去のやりとりを引けます。有効にすると既存の履歴を裏で索引します。",
	},
	{
		Name:        "learning_inbox",
		Title:       "学びの受信箱",
		Description: "ゴールを完了したセッションを安いモデルで振り返り、CLAUDE.md・スキル・メモリへの追記を提案します。採用したものだけが反映されます（CLAUDE.md とスキルは PR、メモリは直接書き込み）。モデルは config.toml の [learning_inbox] model で変更できます（既定 haiku）。",
	},
	{
		Name:        "loops",
		Title:       "ループ",
		Description: "Automation を残タスクがある限り回すループとして使えるようにします。残タスクリスト、各回の結果記録、ダッシュボードが使えるようになります。",
	},
	{
		Name:        "proactive_suggestions",
		Title:       "先回りの提案",
		Description: "複数のセッションにまたがる繰り返しを検知し、『何度も指摘されている注意点をハーネスに書く？』『手で繰り返している作業をループにする？』を通知で聞きます。学びの受信箱が有効なときだけ動きます。",
	},
	{
		Name:        "projects",
		Title:       "プロジェクト単位の表示",
		Description: "セッションをプロジェクト（リポジトリ）単位にまとめ、各プロジェクトのコーディネーター（メインのセッション）を中心に表示します。コーディネーターは配下のループや Automation の状態をツールで把握でき、停止などの知らせを受け取ります。",
	},
	{
		Name:        "choice_cards",
		Title:       "選択肢カード",
		Description: "エージェントや提案ルールが、会話を止めずに選択肢のボタンを出せるようにします。ボタンを押すか、普通に返信するかを選べます。",
	},
}

// Lookup returns the feature with the given name.
func Lookup(name string) (Feature, bool) {
	for _, f := range Features {
		if f.Name == name {
			return f, true
		}
	}
	return Feature{}, false
}

// cache avoids re-parsing config.toml on every call; it reloads when the
// file's mtime or size changes.
var cache struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	size    int64
	flags   map[string]bool
	loaded  bool
}

// Enabled reports whether the named feature is turned on. Unknown features
// and any error while reading the config are treated as off.
func Enabled(name string) bool {
	if _, ok := Lookup(name); !ok {
		return false
	}
	return loadFlags()[name]
}

func loadFlags() map[string]bool {
	path, err := config.ConfigPath()
	if err != nil {
		return nil
	}
	st, statErr := os.Stat(path)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if statErr != nil {
		cache.loaded = false
		return nil
	}
	if cache.loaded && cache.path == path && cache.modTime.Equal(st.ModTime()) && cache.size == st.Size() {
		return cache.flags
	}
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	cache.path, cache.modTime, cache.size, cache.loaded = path, st.ModTime(), st.Size(), true
	cache.flags = cfg.Experimental
	return cache.flags
}

// Status is a feature together with its current on/off state.
type Status struct {
	Feature
	Enabled bool `json:"enabled"`
}

// List returns all known features with their current state.
func List() []Status {
	flags := loadFlags()
	out := make([]Status, 0, len(Features))
	for _, f := range Features {
		out = append(out, Status{Feature: f, Enabled: flags[f.Name]})
	}
	return out
}

// Set turns a known feature on or off and persists it to config.toml.
func Set(name string, enabled bool) error {
	if _, ok := Lookup(name); !ok {
		return fmt.Errorf("unknown experimental feature: %s", name)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Experimental == nil {
		cfg.Experimental = map[string]bool{}
	}
	if enabled {
		cfg.Experimental[name] = true
	} else {
		delete(cfg.Experimental, name)
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	cache.mu.Lock()
	cache.loaded = false
	cache.mu.Unlock()
	return nil
}
