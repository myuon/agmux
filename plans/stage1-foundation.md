# Stage 1: Foundation — プロジェクト構造 + セッション管理

## 目的

Go + TypeScriptのプロジェクト骨格を作り、tmux経由のセッションCRUDを動作させる。
このステージ完了時点で `go run . session create/list/stop` が動く。

## 成果物

- Goプロジェクト構造
- tmuxラッパーパッケージ
- SQLiteによるセッション永続化
- CLIサブコマンドでセッション操作

## タスク

### 1.1 プロジェクト初期化

- `go mod init github.com/myuon/agmux`
- ディレクトリ構造を作成:

```
.
├── cmd/
│   └── agmux/
│       └── main.go          # エントリポイント
├── internal/
│   ├── session/
│   │   ├── manager.go       # セッション管理ロジック
│   │   ├── model.go         # Session struct定義
│   │   └── manager_test.go
│   ├── tmux/
│   │   ├── client.go        # tmux CLI wrapper
│   │   └── client_test.go
│   └── db/
│       ├── sqlite.go         # SQLite初期化・マイグレーション
│       └── sqlite_test.go
├── frontend/                 # (Stage 2で構築)
├── plans/
├── MVP.md
├── go.mod
└── go.sum
```

- 依存追加: `mattn/go-sqlite3`, `stretchr/testify`

### 1.2 tmuxラッパー (`internal/tmux/client.go`)

tmuxコマンドをGoから呼び出すラッパー。すべて `exec.Command` 経由。

```go
type Client struct{}

func (c *Client) NewSession(name string, workDir string, command string) error
// tmux new-session -d -s <name> -c <workDir> <command>

func (c *Client) ListSessions() ([]TmuxSession, error)
// tmux list-sessions -F "#{session_name}:#{session_created}"

func (c *Client) KillSession(name string) error
// tmux kill-session -t <name>

func (c *Client) SendKeys(name string, keys string) error
// tmux send-keys -t <name> <keys> Enter

func (c *Client) CapturePane(name string, lines int) (string, error)
// tmux capture-pane -t <name> -p -S -<lines>

func (c *Client) HasSession(name string) bool
// tmux has-session -t <name>
```

- セッション名プレフィクス: `agmux-` で名前空間を区切る
- テスト: 実際のtmuxを使ったintegration test（`TestNewSession`, `TestSendKeys`, `TestCapturePane`）

### 1.3 SQLiteセットアップ (`internal/db/sqlite.go`)

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    project_path TEXT NOT NULL,
    initial_prompt TEXT,
    tmux_session TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'running',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS daemon_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    action_type TEXT NOT NULL,
    detail TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

- DBファイルパス: `~/.agmux/agmux.db`（デフォルト）
- マイグレーションは起動時に自動実行

### 1.4 セッションマネージャー (`internal/session/manager.go`)

```go
type Manager struct {
    db   *sql.DB
    tmux *tmux.Client
}

func (m *Manager) Create(name, projectPath, prompt string) (*Session, error)
// 1. tmuxセッションを作成（claude codeを起動）
// 2. DBにレコード挿入
// 3. promptがあればsend-keysで送信

func (m *Manager) List() ([]Session, error)
// DBから全セッション取得 + tmux存在確認で状態を補正

func (m *Manager) Stop(id string) error
// 1. tmuxセッションをkill
// 2. DBのstatusを"stopped"に更新

func (m *Manager) Delete(id string) error
// 1. tmuxセッションをkill（存在すれば）
// 2. DBレコード削除

func (m *Manager) SendKeys(id string, text string) error
// tmux send-keys経由でテキスト送信

func (m *Manager) CaptureOutput(id string) (string, error)
// tmux capture-pane経由で画面内容取得
```

- Claude Code起動コマンド: `claude --dangerously-skip-permissions`（MVP用、設定で変更可能に）
- Session IDはUUID v4

### 1.5 CLIエントリポイント (`cmd/agmux/main.go`)

MVP段階ではフラグベースのシンプルなCLI:

```
agmux serve                              # Stage 2以降で実装
agmux session create <name> -p <path> [-m <prompt>]
agmux session list
agmux session stop <id>
agmux session send <id> <text>
agmux session capture <id>
```

- CLIフレームワーク: `cobra` を使用

## 完了条件

- [ ] `go build ./cmd/agmux` が成功する
- [ ] `agmux session create test -p /tmp` でtmuxセッションが作成される
- [ ] `agmux session list` でセッション一覧が表示される
- [ ] `agmux session send <id> "hello"` でテキストが送信される
- [ ] `agmux session capture <id>` で画面内容が表示される
- [ ] `agmux session stop <id>` でセッションが停止する
- [ ] `go test ./...` が通る
