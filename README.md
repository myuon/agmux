# agmux — AI Agent Multiplexer

複数のClaude Codeセッションを同時に実行・監視・制御するための統合管理ツール。
1つのWebダッシュボードから、ローカルPC上で動いているすべてのagentの状態を把握し、介入できます。

## スクリーンショット

### ダッシュボード

![Dashboard](docs/screenshot-dashboard.png)

### セッション詳細 (Stream Mode)

![Session Detail](docs/screenshot-session-detail.png)

## 主な機能

- **セッション管理** — Web UIまたはCLIからClaude Codeセッションを作成・停止・削除
- **リアルタイム監視** — WebSocketで全セッションの状態をリアルタイムに一覧表示
- **自動巡回デーモン** — 各セッションを定期巡回し、承認待ちやエラーを検知して自律的に対応
- **Stream Mode** — Claude CLIのstream-json出力をパース・表示し、ツール呼び出しの詳細まで確認可能
- **Controllerセッション** — agmux自体をClaude Codeで操作するための特別なセッション

## 技術スタック

| カテゴリ | 選定 |
|---------|------|
| バックエンド | Go (chi, cobra, gorilla/websocket, SQLite) |
| フロントエンド | TypeScript + React + Vite + Tailwind CSS |
| セッション管理 | tmux (CLI経由) |
| ビルド | Go embed でsingle binaryにビルド |

## インストール

```bash
go install github.com/myuon/agmux/cmd/agmux@latest
```

前提条件:
- Go 1.21+
- Node.js 18+ (ビルド時のみ)
- tmux

### ソースからビルド

```bash
git clone https://github.com/myuon/agmux.git
cd agmux
make build    # frontend + go build → ./agmux バイナリ生成
make install  # $GOPATH/bin にインストール
```

## 使い方

### サーバー起動

```bash
agmux serve              # デーモン + Web UIを起動 (デフォルト: http://localhost:4321)
agmux serve -p 8080      # ポート指定
```

ブラウザで `http://localhost:4321` を開くとダッシュボードが表示されます。

### CLI

```bash
agmux session list                          # セッション一覧
agmux session create <name> -p <path>       # セッション作成
agmux session create <name> -p <path> -m "prompt"  # 初期プロンプト付き
agmux session create <name> -p <path> --mode stream # Stream Mode
agmux session send <id> "message"           # メッセージ送信
agmux session stop <id>                     # 停止
agmux session delete <id>                   # 削除
```

## 設定

設定ファイルは `~/.agmux/config.toml` に保存されます。Web UIの設定画面からも変更可能です。

```toml
[server]
port = 4321

[daemon]
interval = "30s"

[session]
claude_command = "claude --dangerously-skip-permissions"
```

## Controllerセッション

agmuxはController用の特別なセッションを自動的に起動します。ControllerセッションにClaude Code用のスキルをインストールすると、Controllerから他のセッションを操作できます。

```bash
npx skills add myuon/agmux --skill agmux -y
```

## 開発

```bash
make dev      # フロントエンドdev server + Go run (ホットリロード)
make test     # go test ./...
make build    # プロダクションビルド
```

## ライセンス

MIT
