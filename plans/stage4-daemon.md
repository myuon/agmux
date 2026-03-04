# Stage 4: デーモン + LLM自律判断

## 目的

デーモンループでClaude Sonnet APIを使い、各セッションの状態を判断して自律的に介入する。
このステージ完了時点で、承認待ちの自動解消・エラー復旧・エスカレーションが自動で動作する。

## 成果物

- Anthropic API連携
- LLMによる状態判断+アクション決定
- 自律的アクション実行
- アクション履歴の記録・表示

## タスク

### 4.1 Anthropic APIクライアント (`internal/llm/client.go`)

```go
type Client struct {
    apiKey string
    model  string  // default: "claude-sonnet-4-20250514"
    http   *http.Client
}

func (c *Client) Analyze(prompt string) (*AnalysisResult, error)
// Messages APIを直接呼び出し（軽量にするためSDK不使用）

type AnalysisResult struct {
    Status      string `json:"status"`       // running, waiting, error, done
    Action      string `json:"action"`       // none, approve, retry, escalate
    Reason      string `json:"reason"`       // 判断理由
    SendText    string `json:"send_text"`    // actionがapprove/retryの場合に送るテキスト
}
```

- API Keyは環境変数 `ANTHROPIC_API_KEY` から取得
- タイムアウト: 30秒

### 4.2 判断プロンプト

```
あなたはAI agentセッションの監視デーモンです。
以下のセッションのターミナル出力を分析し、現在の状態と取るべきアクションを判断してください。

## セッション情報
- 名前: {name}
- プロジェクト: {projectPath}
- 前回の状態: {lastStatus}

## ターミナル出力（最新）
```
{capturedOutput}
```

## 判断基準
- agentが正常に動作中 → status: "running", action: "none"
- ユーザーの承認/確認を求めている → status: "waiting", action: "approve", send_textに送るべきテキストを記載
- エラーが発生して停止している → status: "error", action: "retry", send_textにリトライ指示を記載
- 人間の判断が必要な重大な問題 → status: "error", action: "escalate", reasonに理由を記載
- タスクが完了した → status: "done", action: "none"

以下のJSON形式で回答してください:
{ "status": "...", "action": "...", "reason": "...", "send_text": "..." }
```

### 4.3 デーモンループ (`internal/daemon/daemon.go`)

```go
type Daemon struct {
    sessions *session.Manager
    monitor  *monitor.Monitor
    llm      *llm.Client
    hub      *server.Hub
    db       *sql.DB
    interval time.Duration
}

func (d *Daemon) Start(ctx context.Context)
// for-selectループ:
// 1. 全アクティブセッションを取得
// 2. 各セッションのcapture-pane取得
// 3. LLMに状態判断を依頼
// 4. 結果に基づきアクション実行
// 5. アクション履歴をDBに記録
// 6. WebSocketでブロードキャスト
// 7. interval待機
```

- LLM呼び出しは各セッションに対して並行実行（goroutine + errgroup）
- 前回と同じ状態なら判断スキップ（出力が変わっていなければ）
- LLM API失敗時はログに記録してスキップ（次の巡回で再試行）

### 4.4 アクション実行

```go
func (d *Daemon) executeAction(session *Session, result *AnalysisResult) error {
    switch result.Action {
    case "approve":
        return d.sessions.SendKeys(session.ID, result.SendText)
    case "retry":
        return d.sessions.SendKeys(session.ID, result.SendText)
    case "escalate":
        return d.recordEscalation(session, result.Reason)
    case "none":
        return nil
    }
}
```

### 4.5 アクション履歴テーブル・API

既存の `daemon_actions` テーブルに記録:

```go
type DaemonAction struct {
    ID        int
    SessionID string
    Action    string  // approve, retry, escalate, none
    Detail    string  // LLMの判断理由 + 送信テキスト
    CreatedAt time.Time
}
```

APIエンドポイント追加:

```
GET /api/actions                → 全アクション履歴（最新50件）
GET /api/sessions/:id/actions   → セッション別アクション履歴
```

### 4.6 フロントエンド: アクション履歴表示

- ダッシュボードにアクションログパネルを追加
- セッション詳細にもアクション履歴を表示
- エスカレーションは目立つ通知（赤バッジ）で表示
- WebSocketの `action_log` メッセージでリアルタイム更新

## 完了条件

- [ ] デーモンが設定間隔で全セッションを巡回する
- [ ] LLMが各セッションの状態を正しく判断する
- [ ] 承認待ちセッションに自動でapproveが送信される
- [ ] エラー状態のセッションにリトライ指示が送信される
- [ ] エスカレーションがダッシュボードに通知される
- [ ] アクション履歴がDBに記録されダッシュボードで閲覧できる
- [ ] LLM API障害時にデーモンがクラッシュしない
