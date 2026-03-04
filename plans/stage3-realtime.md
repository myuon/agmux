# Stage 3: リアルタイム状態監視 + WebSocket

## 目的

セッションの状態をリアルタイムに検知し、WebSocket経由でダッシュボードに即座に反映する。
このステージ完了時点で、ダッシュボードがポーリングなしでセッション状態をリアルタイム更新する。

## 成果物

- 状態分類ロジック
- WebSocketサーバー
- ダッシュボードのリアルタイム更新
- セッション詳細画面（出力表示）

## タスク

### 3.1 状態分類 (`internal/monitor/monitor.go`)

```go
type SessionStatus string

const (
    StatusRunning  SessionStatus = "running"   // agentが実行中
    StatusWaiting  SessionStatus = "waiting"   // ユーザー入力/承認待ち
    StatusError    SessionStatus = "error"     // エラーで停止
    StatusDone     SessionStatus = "done"      // 完了
    StatusStopped  SessionStatus = "stopped"   // 手動停止
)

type Monitor struct {
    tmux *tmux.Client
}

func (m *Monitor) CheckStatus(session *Session) (SessionStatus, string, error)
// 1. tmux has-sessionで存在確認 → なければStopped
// 2. capture-paneで画面内容取得
// 3. パターンマッチで状態判定:
//    - "Do you want to proceed?" / "Allow" / "[Y/n]" → Waiting
//    - "Error" / "failed" / "panic" → Error
//    - 入力プロンプト表示中（$やclaude>） → Done or Waiting
//    - それ以外 → Running
// 返り値: (status, capturedOutput, error)
```

- 初期実装はパターンマッチで十分（Stage 4でLLM判断に強化）
- Claude Code特有のプロンプトパターンを検出

### 3.2 WebSocketハブ (`internal/server/ws.go`)

```go
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan Message
    register   chan *Client
    unregister chan *Client
}

type Message struct {
    Type string      `json:"type"`    // "session_update", "action_log", etc.
    Data interface{} `json:"data"`
}
```

- エンドポイント: `GET /ws`
- メッセージタイプ:
  - `session_update`: セッション状態が変わった時
  - `session_output`: セッションの出力更新
  - `action_log`: デーモンアクション発生時（Stage 4）

### 3.3 定期状態チェッカー

```go
type StatusChecker struct {
    monitor  *Monitor
    sessions *session.Manager
    hub      *Hub
    interval time.Duration
}

func (sc *StatusChecker) Start(ctx context.Context)
// ticker.Cでループ
// 全セッションの状態をチェック
// 変化があればDBを更新しWebSocketでブロードキャスト
```

- interval: 5秒（状態表示用の軽量チェック。デーモンの判断間隔とは別）
- capture-paneの結果はメモリにキャッシュし、UIからのリクエストに即答

### 3.4 フロントエンドWebSocket対応

```typescript
// src/hooks/useWebSocket.ts
function useWebSocket(url: string) {
    // 接続管理、再接続ロジック
    // メッセージ受信時にコールバック呼び出し
}

// App.tsxでセッション一覧の状態をWS経由で更新
```

### 3.5 セッション詳細画面

**セッション詳細ページ:**

```
┌─────────────────────────────────────────────┐
│  ← Back    session-1    🟢 running          │
├─────────────────────────────────────────────┤
│  Project: /path/to/project                  │
│  Created: 2024-01-01 12:00                  │
│  Status: running                            │
├─────────────────────────────────────────────┤
│  Terminal Output:                           │
│  ┌────────────────────────────────────────┐ │
│  │ $ claude --dangerously-skip-perms...   │ │
│  │ > Working on task...                   │ │
│  │ > Editing file src/main.go...          │ │
│  │ > ...                                  │ │
│  └────────────────────────────────────────┘ │
├─────────────────────────────────────────────┤
│  Send message: [________________] [Send]    │
└─────────────────────────────────────────────┘
```

- capture-paneの内容をANSIカラー付きで表示（xterm.jsを使用）
- 手動メッセージ送信フォーム
- 自動スクロール

## 完了条件

- [ ] セッション状態が自動検知される（running/waiting/error/done/stopped）
- [ ] ダッシュボードがWebSocketでリアルタイム更新される
- [ ] セッション詳細画面でターミナル出力が表示される
- [ ] セッション詳細画面からメッセージを手動送信できる
- [ ] ブラウザをリロードなしでセッション状態が変化する
