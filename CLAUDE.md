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
- issueに取り掛かるときは、agentに作業をdispatchするとよい。また、その際にはworktreeを使って作業が干渉しないようにすること
- 動作確認するときは、PRのブランチをチェックアウトし、`make restart` でサーバーを読み込んでから動作確認に入る。処理の確認には agmux CLI を利用し、Webアプリの画面を見る必要があるときは agent browser を利用する。どちらもskillがあるので利用する際には参照すること

## 仕様策定ルール

- `/spec` スキルで仕様を詰める際、Spec.md ファイルは作成しない。代わりに **イシューの body に仕様を記載する**
- 複数ステップからなるタスクの場合、`gh` の **sub-issue 機能**を使って親子の issue を紐づける
  - 親イシュー: 全体の仕様・ゴールを記載
  - 子イシュー (sub-issue): 各ステップ・タスクを個別のイシューとして起票し、親に紐づける
