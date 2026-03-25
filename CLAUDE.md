# CLAUDE.md

## プロジェクト概要

agmux - 複数のAI agentセッションを同時に実行・監視・制御するための統合管理ツール。
詳細は MVP.md を参照。

## ビルド・テスト

```bash
make build        # frontend + go build
make test         # go test ./...
make dev          # dev mode (frontend dev server + go run)
```

## サーバー

- アプリはポート **4321** で動作する (`http://localhost:4321`)
- launchd でデーモン管理されている（サービス名: `com.myuon.agmux`）

## デプロイ手順（PRマージ後）

```bash
git checkout main && git pull   # 最新を取得
make build                      # frontend + go build
go install ./...                # バイナリをインストール
launchctl kickstart -k gui/$(id -u)/com.myuon.agmux  # サーバー再起動
```

## イシュー管理

- イシュー管理は **gh issue** (GitHub Issues) を全面的に使用する
- 新規イシューは `gh issue create` で起票
- 進捗管理は `gh issue list` で確認
- 複数イシューの振り分け・実装は `/dispatch` スキルを使う
- 動作確認するときは、PRのブランチをチェックアウトし、`make build && go install ./...` してから `launchctl kickstart -k gui/$(id -u)/com.myuon.agmux` でサーバーを再起動して動作確認に入る。処理の確認には agmux CLI を利用し、Webアプリの画面を見る必要があるときは agent browser を利用する。どちらもskillがあるので利用する際には参照すること
- **フロントエンドのみの変更の場合**: configによりfrontendの `dist` ディレクトリを直接配信する設定になっているため、PRブランチをチェックアウトして `make build` するだけで画面に反映される（サーバー再起動不要）

## エージェントの自律性ルール

- `make build`, コンフリクト解消など技術的に自明な作業は自己判断で進める
- 動作確認も可能な限り自分で行う（agmux CLI、agent-browser等を活用）
- escalateは「ユーザーの判断が本当に必要なとき」のみ使う（設計判断、目視確認が不可避な場合など）
- 「〇〇しますか？」「〇〇していいですか？」は基本不要。やるべきことは黙ってやる
- ただしマージ・プッシュなど外部影響のある操作は確認を取る

## コンテキスト節約ルール

- ファイル内の特定箇所を探すときは **Read ではなく Grep** を使う（Read: 〜3,000トークン → Grep: 〜200トークン）
- 大きなファイル（200行超）を Read するときは **offset / limit パラメータ** で必要な範囲だけ読む
- コードベースの探索・調査は **サブエージェント（`subagent_type: "Explore"`）** に委譲し、メインコンテキストを汚さない
- ファイル全体を理解する必要がない場合、まず Grep で該当箇所を特定してから、その周辺だけ Read する

## 仕様策定ルール

- `/spec` スキルで仕様を詰める際、Spec.md ファイルは作成しない。代わりに **イシューの body に仕様を記載する**
- 複数ステップからなるタスクの場合、`gh` の **sub-issue 機能**を使って親子の issue を紐づける
  - 親イシュー: 全体の仕様・ゴールを記載
  - 子イシュー (sub-issue): 各ステップ・タスクを個別のイシューとして起票し、親に紐づける
