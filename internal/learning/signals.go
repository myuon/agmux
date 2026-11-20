package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/myuon/agmux/internal/automation"
)

// ProactiveFeatureName is the experimental flag for repeated-pattern
// suggestions. It only works while FeatureName (learning_inbox) is on too.
const ProactiveFeatureName = "proactive_suggestions"

// Signal kinds.
const (
	SignalCorrection = "correction"
	SignalTask       = "task"
)

// Cluster states.
const (
	clusterOpen      = "open"
	clusterProposed  = "proposed"
	clusterDismissed = "dismissed"
)

const (
	maxClusterCandidates = 30
	maxSignalsPerReview  = 6
	correctionThreshold  = 2 // distinct sessions
	taskThreshold        = 3 // distinct manually-started sessions
	maxExcerptRunes      = 300
)

// Signal is something a review noticed that may repeat across sessions: a
// correction the user made, or a kind of task the user asked for.
type Signal struct {
	ID          int64     `json:"id"`
	ProjectPath string    `json:"projectPath"`
	SessionID   string    `json:"sessionId"`
	SessionName string    `json:"sessionName,omitempty"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary"`
	Excerpt     string    `json:"excerpt"`
	ClusterID   int64     `json:"clusterId"`
	CreatedAt   time.Time `json:"createdAt"`
}

type cluster struct {
	ID      int64
	Summary string
	State   string
}

func initSignalTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS learning_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_path TEXT NOT NULL,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			excerpt TEXT NOT NULL DEFAULT '',
			cluster_id INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_learning_signals_cluster ON learning_signals(cluster_id);
		CREATE TABLE IF NOT EXISTS learning_clusters (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_path TEXT NOT NULL,
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'open',
			created_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_learning_clusters_project ON learning_clusters(project_path, kind);
	`)
	return err
}

// InsertSignal stores a signal that is not yet clustered.
func (s *Store) InsertSignal(sig *Signal) error {
	sig.CreatedAt = time.Now()
	res, err := s.db.Exec(`INSERT INTO learning_signals (project_path, session_id, kind, summary, excerpt, cluster_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sig.ProjectPath, sig.SessionID, sig.Kind, sig.Summary, sig.Excerpt, sig.ClusterID, sig.CreatedAt)
	if err != nil {
		return err
	}
	sig.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) setSignalCluster(signalID, clusterID int64) error {
	_, err := s.db.Exec(`UPDATE learning_signals SET cluster_id = ? WHERE id = ?`, clusterID, signalID)
	return err
}

func (s *Store) createCluster(project, kind, summary string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO learning_clusters (project_path, kind, summary, state, created_at) VALUES (?, ?, ?, ?, ?)`,
		project, kind, summary, clusterOpen, time.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) setClusterState(id int64, state string) error {
	_, err := s.db.Exec(`UPDATE learning_clusters SET state = ? WHERE id = ?`, state, id)
	return err
}

func (s *Store) clusterState(id int64) (string, error) {
	var st string
	err := s.db.QueryRow(`SELECT state FROM learning_clusters WHERE id = ?`, id).Scan(&st)
	return st, err
}

// recentClusters returns the newest clusters of a project and kind; all
// states are included so that a dismissed topic keeps absorbing its repeats.
func (s *Store) recentClusters(project, kind string, limit int) ([]cluster, error) {
	rows, err := s.db.Query(`SELECT id, summary, state FROM learning_clusters WHERE project_path = ? AND kind = ? ORDER BY id DESC LIMIT ?`, project, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cluster
	for rows.Next() {
		var c cluster
		if err := rows.Scan(&c.ID, &c.Summary, &c.State); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const signalCols = `sg.id, sg.project_path, sg.session_id, COALESCE(se.name, ''), sg.kind, sg.summary, sg.excerpt, sg.cluster_id, sg.created_at`

// ClusterSignals returns a cluster's signals, oldest first.
func (s *Store) ClusterSignals(clusterID int64) ([]*Signal, error) {
	rows, err := s.db.Query(`SELECT `+signalCols+` FROM learning_signals sg LEFT JOIN sessions se ON se.id = sg.session_id WHERE sg.cluster_id = ? ORDER BY sg.id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Signal
	for rows.Next() {
		var g Signal
		if err := rows.Scan(&g.ID, &g.ProjectPath, &g.SessionID, &g.SessionName, &g.Kind, &g.Summary, &g.Excerpt, &g.ClusterID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) attachEvidence(p *Proposal) {
	if p.ClusterID == 0 {
		return
	}
	if sigs, err := s.ClusterSignals(p.ClusterID); err == nil {
		p.Evidence = sigs
	}
}

func distinctSessions(sigs []*Signal) int {
	seen := map[string]bool{}
	for _, g := range sigs {
		seen[g.SessionID] = true
	}
	return len(seen)
}

// extractJSON finds the first JSON object in the reviewer output, skipping
// code fences, surrounding prose and anything (tags, more text, a second
// object) after it. When no object decodes, the trimmed output is returned so
// the caller's json.Unmarshal reports the error.
func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if m := codeFence.FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(raw[i:]))
		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			continue
		}
		return raw[i : i+int(dec.InputOffset())]
	}
	return raw
}

// ParseSignals extracts the optional "signals" array from reviewer output.
// Output without signals (or with malformed ones) yields none.
func ParseSignals(raw string) []*Signal {
	var out struct {
		Signals []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
			Excerpt string `json:"excerpt"`
		} `json:"signals"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &out); err != nil {
		return nil
	}
	var res []*Signal
	for _, g := range out.Signals {
		g.Summary = strings.TrimSpace(g.Summary)
		if g.Summary == "" || (g.Kind != SignalCorrection && g.Kind != SignalTask) {
			continue
		}
		res = append(res, &Signal{Kind: g.Kind, Summary: g.Summary, Excerpt: headRunes(strings.TrimSpace(g.Excerpt), maxExcerptRunes)})
		if len(res) >= maxSignalsPerReview {
			break
		}
	}
	return res
}

func (r *Reviewer) proactiveOn() bool {
	return r.Proactive != nil && r.Proactive()
}

const signalsInstruction = `
## シグナル（任意）
proposals とは別に、複数セッションにまたがる繰り返しの検知に使う「シグナル」を、出力 JSON の "signals" に入れてください。
- kind が "correction": ユーザーがエージェントのやり方を直した・指摘した箇所。summary は「何をどう直されたか」を、このセッション固有の固有名詞を落とした一般形で1文
- kind が "task": ユーザーが依頼した作業の型。summary は固有名詞（PR 番号、ファイル名など）を落とした一般形で1文（例: 「依存パッケージを更新して PR を作る」）
- excerpt: 根拠になったユーザー発言の短い抜粋（そのまま、300字以内）
- 該当が無ければ空配列。最大 6 件
出力は {"proposals":[...],"signals":[{"kind":"correction|task","summary":"…","excerpt":"…"}]} の形にする。
`

// processSignals stores the signals of a review, clusters them with earlier
// signals of the same project and raises suggestions that reached their
// threshold. Task signals from automation sessions are not counted.
func (r *Reviewer) processSignals(ctx context.Context, sess *sessionInfo, signals []*Signal) {
	automated := false
	var automationID sql.NullString
	if err := r.db.QueryRow(`SELECT automation_id FROM sessions WHERE id = ?`, sess.id).Scan(&automationID); err == nil && automationID.Valid && automationID.String != "" {
		automated = true
	}
	touched := map[int64]string{} // cluster id -> kind
	for _, sig := range signals {
		if sig.Kind == SignalTask && automated {
			continue
		}
		sig.ProjectPath, sig.SessionID = sess.projectPath, sess.id
		if err := r.store.InsertSignal(sig); err != nil {
			r.logger.Warn("learning: insert signal failed", "error", err)
			continue
		}
		cid, err := r.assignCluster(ctx, sig)
		if err != nil {
			r.logger.Warn("learning: cluster failed", "error", err)
			continue
		}
		touched[cid] = sig.Kind
	}
	for cid, kind := range touched {
		if err := r.maybeSuggest(ctx, sess, cid, kind); err != nil {
			r.logger.Warn("learning: suggest failed", "clusterId", cid, "error", err)
		}
	}
}

// assignCluster puts the signal into an existing cluster of the same
// project and kind when the model judges it the same point, else a new one.
func (r *Reviewer) assignCluster(ctx context.Context, sig *Signal) (int64, error) {
	cands, err := r.store.recentClusters(sig.ProjectPath, sig.Kind, maxClusterCandidates)
	if err != nil {
		return 0, err
	}
	var chosen int64
	if len(cands) > 0 {
		raw, err := r.Generate(ctx, r.Model(), clusterPrompt(cands, sig))
		if err != nil {
			r.logger.Warn("learning: cluster judgement failed; treating as new", "error", err)
		} else {
			chosen = parseClusterChoice(raw, cands)
		}
	}
	if chosen == 0 {
		chosen, err = r.store.createCluster(sig.ProjectPath, sig.Kind, sig.Summary)
		if err != nil {
			return 0, err
		}
	}
	sig.ClusterID = chosen
	return chosen, r.store.setSignalCluster(sig.ID, chosen)
}

func clusterPrompt(cands []cluster, sig *Signal) string {
	var b strings.Builder
	for _, c := range cands {
		fmt.Fprintf(&b, "- %d: %s\n", c.ID, c.Summary)
	}
	what := "ユーザーの訂正"
	if sig.Kind == SignalTask {
		what = "ユーザーが依頼した作業の型"
	}
	return fmt.Sprintf(`次の「新しいシグナル」（%s）が、候補クラスタのどれかと同じ趣旨かを判定してください。
言い回しや固有名詞が違っても、同じ指摘・同じ種類の作業なら同じ趣旨です。迷う場合は "new" にしてください。

## 候補クラスタ（id: 代表要約）
%s
## 新しいシグナル
%s

JSON だけを出力する（前置き・コードフェンス不要）。
{"cluster_id": 候補の id（数値）, または "new"}
`, what, b.String(), sig.Summary)
}

func parseClusterChoice(raw string, cands []cluster) int64 {
	var out struct {
		ClusterID json.RawMessage `json:"cluster_id"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &out); err != nil {
		return 0
	}
	var id int64
	if err := json.Unmarshal(out.ClusterID, &id); err != nil {
		var s string
		if json.Unmarshal(out.ClusterID, &s) != nil {
			return 0
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &id); err != nil {
			return 0
		}
	}
	for _, c := range cands {
		if c.ID == id {
			return id
		}
	}
	return 0
}

// maybeSuggest creates a proposal when the cluster reached its threshold and
// has neither been proposed nor dismissed.
func (r *Reviewer) maybeSuggest(ctx context.Context, sess *sessionInfo, clusterID int64, kind string) error {
	state, err := r.store.clusterState(clusterID)
	if err != nil || state != clusterOpen {
		return err
	}
	sigs, err := r.store.ClusterSignals(clusterID)
	if err != nil || len(sigs) == 0 {
		return err
	}
	n := distinctSessions(sigs)
	var p *Proposal
	switch kind {
	case SignalCorrection:
		if n < correctionThreshold {
			return nil
		}
		p, err = r.buildCorrectionProposal(ctx, sess, sigs)
	case SignalTask:
		if n < taskThreshold {
			return nil
		}
		p, err = r.buildLoopProposal(ctx, sess, sigs)
	}
	if err != nil || p == nil {
		return err
	}
	last := sigs[len(sigs)-1]
	p.SessionID, p.SessionName, p.ProjectPath = last.SessionID, last.SessionName, sess.projectPath
	p.ClusterID = clusterID
	if err := r.store.Insert(p); err != nil {
		return err
	}
	if err := r.store.setClusterState(clusterID, clusterProposed); err != nil {
		return err
	}
	if r.OnSuggestion != nil {
		r.OnSuggestion(p, sigs[0].Summary, n)
	}
	return nil
}

func evidenceList(sigs []*Signal) string {
	var b strings.Builder
	for _, g := range sigs {
		fmt.Fprintf(&b, "- (セッション %q) %s\n  抜粋: %s\n", g.SessionName, g.Summary, oneLine(g.Excerpt))
	}
	return b.String()
}

func (r *Reviewer) buildCorrectionProposal(ctx context.Context, sess *sessionInfo, sigs []*Signal) (*Proposal, error) {
	prompt := fmt.Sprintf(correctionPrompt,
		sess.projectPath,
		readOr(filepath.Join(sess.projectPath, "CLAUDE.md"), "(CLAUDE.md なし)", maxClaudeMDRunes),
		listSkills(sess.projectPath),
		readOr(filepath.Join(MemoryDir(r.homeDir, sess.projectPath), "MEMORY.md"), "(メモリなし)", maxMemoryIndexRunes),
		evidenceList(sigs),
	)
	raw, err := r.Generate(ctx, r.Model(), prompt)
	if err != nil {
		return nil, err
	}
	ps, err := ParseProposals(raw)
	if err != nil || len(ps) == 0 {
		return nil, err
	}
	p := ps[0]
	p.Origin = OriginRepeatedCorrection
	if p.Reason == "" {
		p.Reason = fmt.Sprintf("%d セッションで同じ趣旨の指摘がありました", distinctSessions(sigs))
	}
	return p, nil
}

const correctionPrompt = `複数のセッションで、ユーザーがエージェントに同じ趣旨の訂正を繰り返しています。
これを、次回以降は指摘されなくて済むように、CLAUDE.md・スキル・メモリのどれかに書く1件の提案にしてください。

ルール:
- "lessons, not logs": 事件の経緯や固有の値は書かず、一般化したルールとして書く。短く具体的に
- 既存の CLAUDE.md・スキル・メモリにすでに書かれているなら、提案は空配列でよい
- 全員に常に適用すべきルールは claude_md、特定の手順に紐づくなら skill、個人の好みや事実なら memory
- 既存の文書と同じ言語で書く

## プロジェクト
%s

## 既存の CLAUDE.md
<claude_md>
%s
</claude_md>

## 既存のスキル
%s

## 既存のメモリ（索引）
<memory_index>
%s
</memory_index>

## 繰り返されている訂正
%s
## 出力形式
JSON だけを出力する（前置き・コードフェンス不要）。proposals は最大1件。
{"proposals":[{"target":"claude_md|skill|memory","name":"skill名 または memory の slug（kebab-case）。claude_md では空","memory_type":"memory のときだけ: user|feedback|project|reference","title":"一行の要約","content":"追記する本文（Markdown）","reason":"なぜ提案するか。根拠となった指摘を短く引用"}]}
`

func (r *Reviewer) loopsOn() bool { return r.LoopsEnabled != nil && r.LoopsEnabled() }

func (r *Reviewer) buildLoopProposal(ctx context.Context, sess *sessionInfo, sigs []*Signal) (*Proposal, error) {
	queueNote := "queue は使えません（interval か cron を選ぶ）"
	if r.loopsOn() {
		queueNote = "queue は「残タスクがある限り回す」型の作業のときだけ選ぶ（trigger_value は実行間の待ち時間、例 0s）"
	}
	prompt := fmt.Sprintf(loopPrompt, sess.projectPath, queueNote, evidenceList(sigs))
	raw, err := r.Generate(ctx, r.Model(), prompt)
	if err != nil {
		return nil, err
	}
	var out struct {
		Name         string `json:"name"`
		Prompt       string `json:"prompt"`
		TriggerType  string `json:"trigger_type"`
		TriggerValue string `json:"trigger_value"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &out); err != nil {
		return nil, fmt.Errorf("parse loop proposal: %w", err)
	}
	out.Name, out.Prompt = strings.TrimSpace(out.Name), strings.TrimSpace(out.Prompt)
	if out.Name == "" || out.Prompt == "" {
		return nil, nil
	}
	tt, tv := automation.TriggerType(out.TriggerType), strings.TrimSpace(out.TriggerValue)
	if automation.ValidateTrigger(tt, tv) != nil || (tt == automation.TriggerQueue && !r.loopsOn()) {
		// The person reviews the trigger before enabling, so fall back to a
		// plain daily interval rather than dropping the suggestion.
		tt, tv = automation.TriggerInterval, "24h"
	}
	reason := strings.TrimSpace(out.Reason)
	if reason == "" {
		reason = fmt.Sprintf("%d 回手で依頼されています", distinctSessions(sigs))
	}
	return &Proposal{
		Origin: OriginRepeatedTask, Target: TargetLoop,
		Title: out.Name, Content: out.Prompt, Reason: reason,
		TriggerType: string(tt), TriggerValue: tv,
	}, nil
}

const loopPrompt = `ユーザーが、同じ型の作業を何度も手で依頼しています。これを Automation（繰り返し実行）にするための案を作ってください。

- name: Automation の名前（短く）
- prompt: 毎回エージェントに渡す依頼文。依頼ごとに変わる固有の値（PR 番号など）は含めず、一般形で、自己完結した指示にする
- trigger_type: "interval"（trigger_value は 6h や 30m）または "cron"（5 フィールド、例 0 9 * * 1-5）。%[2]s
- 依頼の頻度や性質から、合うトリガーを選ぶ

## プロジェクト
%[1]s

## 手で依頼された作業
%[3]s
JSON だけを出力する（前置き・コードフェンス不要）。
{"name":"…","prompt":"…","trigger_type":"interval|cron|queue","trigger_value":"…","reason":"なぜこのトリガーか。一文"}
`
