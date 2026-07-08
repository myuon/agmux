---
name: dev-verify
description: agmuxの変更を本番インスタンス(4321)に影響させずに画面・動作確認する手順。UIの変更確認、セッション作成などバックエンドAPIの動作確認、確認結果のスクショをPR/issueに添付するときに使う。
---

# dev-verify — 本番に影響させない動作確認

本番の agmux は launchd 管理でポート **4321** で動作し、config により `frontend/dist` を直接配信している。
そのため **`make build` / `make reload-frontend` を実行すると本番画面が即座に書き換わる**。確認が終わって PR がマージされるまでビルドしないこと。

## 1. フロントエンドのみの変更

Vite dev サーバー（HMR）で確認する。`dist` に書き込まないので本番画面は変わらない。

```bash
cd frontend && npm run dev   # http://localhost:5173
```

- `/api`・`/ws` は本番バックエンド(4321)にプロキシされるため、本番と同じデータで UI だけ差し替えて確認できる
- **読み取り表示の確認のみに使うこと**。5173 経由でもセッション作成等の書き込み操作は本番 DB に反映されるので、書き込みを伴う確認は隔離バックエンド（下記）に向ける

## 2. バックエンドも変更する場合（隔離環境）

`~/.agmux/server.lock` はポートに関係なくグローバルなので、ポート変更だけでは二重起動できない。
**HOME を差し替えて完全隔離**で起動する（DB・ロック・streams・workspaces すべて分離される）:

```bash
# ビルドは通常の HOME で行う（HOME=... go run は Go モジュールを全再DLしてしまう）
go build -o /tmp/agmux-dev ./cmd/agmux
HOME=/tmp/agmux-dev-home /tmp/agmux-dev serve --dev --port 4322 &

# Vite のプロキシ先を隔離バックエンドに向ける
cd frontend && AGMUX_BACKEND_PORT=4322 npm run dev
```

- API 単体の確認は `curl http://localhost:4322/api/...` で直接叩ける
- 隔離環境ではセッション作成・削除など破壊的操作も自由に行ってよい

## 3. 画面確認とスクショ

画面操作・確認は **agent-browser** skill を使う:

```bash
agent-browser open http://localhost:5173
agent-browser wait --load networkidle
agent-browser snapshot -i          # 要素の ref を取得して操作
agent-browser screenshot /tmp/shot.png
```

スクショは **uishot CLI** で R2 にアップロードし、URL を PR / issue に貼る
（MCP の `upload_screenshot` は base64 をコンテキストに載せてしまうため CLI を使うこと）:

```bash
# アップロード前に縮小するとリンク先も軽い（任意）
sips -Z 840 -s format jpeg -s formatOptions 65 /tmp/shot.png --out /tmp/shot.jpg

URL=$(uishot upload --pr <PR番号> --name <名前> --file /tmp/shot.jpg)   # --issue <番号> も可
gh pr comment <PR番号> --body "![shot]($URL)"
```

## 4. 後片付け

```bash
lsof -ti :5173 | xargs kill        # Vite 停止
pkill -f "/tmp/agmux-dev"          # 隔離バックエンド停止（holder プロセスが残りやすいので -f で拾う）
agent-browser close                # ブラウザ終了
chmod -R +w /tmp/agmux-dev-home && rm -rf /tmp/agmux-dev-home /tmp/agmux-dev
lsof -ti :4321 >/dev/null && echo "本番は無事"
```
