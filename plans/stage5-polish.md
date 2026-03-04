# Stage 5: 設定 + 統合仕上げ

## 目的

設定ファイル対応、UI仕上げ、single binaryビルドの最終確認を行い、MVPを完成させる。

## 成果物

- TOML設定ファイル
- 設定のWeb UI表示
- 完成したsingle binary
- 全Acceptance Criteria達成

## タスク

### 5.1 設定ファイル (`internal/config/config.go`)

```toml
# ~/.agmux/config.toml

[server]
port = 4321

[daemon]
interval = "30s"
auto_approve = true

[llm]
model = "claude-sonnet-4-20250514"
# api_keyは環境変数 ANTHROPIC_API_KEY から取得

[session]
claude_command = "claude --dangerously-skip-permissions"
```

```go
type Config struct {
    Server  ServerConfig
    Daemon  DaemonConfig
    LLM     LLMConfig
    Session SessionConfig
}

func Load() (*Config, error)
// 1. デフォルト値をセット
// 2. ~/.agmux/config.toml があれば読み込み上書き
// 3. 環境変数で上書き
```

- ライブラリ: `BurntSushi/toml`
- 設定ファイルがなければデフォルト値で動作

### 5.2 設定をアプリ全体に適用

- `agmux serve` 起動時にConfigをロードし、各コンポーネントに渡す
- daemon interval、LLM model、server port等を設定値で初期化

### 5.3 UIの仕上げ

- レスポンシブレイアウト調整
- セッションカードのステータスバッジ色
  - running: 緑
  - waiting: 黄
  - error: 赤
  - done: 青
  - stopped: グレー
- エスカレーション通知のバッジ表示
- セッション作成フォームのバリデーション
- エラー表示（API呼び出し失敗時）

### 5.4 Makefile最終化

```makefile
.PHONY: build dev clean

build: build-frontend
	go build -o agmux ./cmd/agmux

build-frontend:
	cd frontend && npm ci && npm run build

dev:
	# フロントエンドとバックエンドを並行起動
	cd frontend && npm run dev &
	go run ./cmd/agmux serve --dev

clean:
	rm -f agmux
	rm -rf frontend/dist

test:
	go test ./...
	cd frontend && npm test
```

### 5.5 統合テスト

手動で以下のシナリオを通しで確認:

1. **基本フロー**: `make build` → `./agmux serve` → ブラウザでセッション作成 → 実行確認 → 停止
2. **デーモン介入**: セッション作成 → 承認待ち発生 → デーモンが自動承認 → 続行確認
3. **複数セッション**: 5セッション同時作成 → 全セッションがダッシュボードに表示 → 各状態がリアルタイム更新

## 完了条件

- [ ] `~/.agmux/config.toml` で設定が変更できる
- [ ] 設定ファイルなしでもデフォルト値で動作する
- [ ] `make build` で single binary がビルドされる
- [ ] ビルドしたバイナリ単体で全機能が動作する
- [ ] MVP.md の Acceptance Criteria 10項目が全て達成されている
