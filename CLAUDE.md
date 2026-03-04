# CLAUDE.md

## プロジェクト概要

agmux - 複数のAI agentセッションを同時に実行・監視・制御するための統合管理ツール。
詳細は MVP.md を参照。

## 技術スタック

- バックエンド: Go (chi, cobra, gorilla/websocket, SQLite)
- フロントエンド: TypeScript + React + Vite + Tailwind CSS
- ビルド: `make build` で single binary にビルド（Go embed）

## ビルド・テスト

```bash
make build        # frontend + go build
make test         # go test ./...
make dev          # dev mode (frontend dev server + go run)
```

## イシュー管理

- イシュー管理は **gh issue** (GitHub Issues) を全面的に使用する
- 新規イシューは `gh issue create` で起票
- 進捗管理は `gh issue list` で確認
