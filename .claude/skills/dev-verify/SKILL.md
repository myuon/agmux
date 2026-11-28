---
name: dev-verify
description: agmuxの変更を本番インスタンス(4321)に影響させずに画面・動作確認する手順。UIの変更確認、セッション作成などバックエンドAPIの動作確認、確認結果のスクショをPR/issueに添付するときに使う。
---

# dev-verify — 本番に影響させない動作確認

本番の agmux は launchd 管理でポート **4321** で動作する。フロントエンドは binary に embed されて
配信されるので（`frontend_dir` による dist 直接配信は 2026-07-14 に廃止）、`make build` しても
稼働中の本番画面は変わらない。本番に反映されるのはリリース + `agmux update` を実行したときだけ。

一方で **データは共有される**。5173 の Vite dev サーバーは既定で本番バックエンド(4321)にプロキシするため、
書き込みを伴う確認は必ず隔離バックエンド（下記）に向けること。

## 1. フロントエンドのみの変更

Vite dev サーバー（HMR）で確認する。

```bash
cd frontend && npm run dev   # http://localhost:5173
```

- `/api`・`/ws` は本番バックエンド(4321)にプロキシされるため、本番と同じデータで UI だけ差し替えて確認できる
- **読み取り表示の確認のみに使うこと**。5173 経由でもセッション作成等の書き込み操作は本番 DB に反映される

### 罠: Vite dev 経由の PUT などが 403 になる

- Vite dev サーバー経由では、PUT など状態を変える API が cross-site ガードで 403 になることがある（本番の embedded 配信では起きない想定）。
- 書き込み API は curl で直接叩いて動作を確認する（隔離バックエンドなら `curl http://localhost:4322/api/...`）。UI からの書き込みは embedded 配信（本番相当）の環境で確認する。
- 確認できなかった経路は PR 本文に明記する。

## 2. バックエンドも変更する場合（隔離環境）

`~/.agmux/server.lock` はポートに関係なくグローバルなので、ポート変更だけでは二重起動できない。
**HOME と TMPDIR を差し替えて完全隔離**で起動する（DB・ロック・streams・workspaces・holder socket すべて分離される）:

```bash
# ビルドは通常の HOME で行う（HOME=... go run は Go モジュールを全再DLしてしまう）
go build -o /tmp/agmux-dev ./cmd/agmux

# 隔離 HOME 側の config にポートを書く（後述の MCP URL の罠を回避するため必須）
mkdir -p /tmp/agmux-dev-home/.agmux
printf '[server]\nport = 4322\n' > /tmp/agmux-dev-home/.agmux/config.toml

HOME=/tmp/agmux-dev-home TMPDIR=/tmp/agmux-dev-tmp /tmp/agmux-dev serve --dev --port 4322 &

# Vite のプロキシ先を隔離バックエンドに向ける
cd frontend && AGMUX_BACKEND_PORT=4322 npm run dev
```

- API 単体の確認は `curl http://localhost:4322/api/...` で直接叩ける
- 隔離環境ではセッション作成・削除など破壊的操作も自由に行ってよい

### 罠1: MCP URL が本番(4321)を向く

`internal/session/mcp_config.go` の `writeMCPConfig` は `--port` フラグではなく **config の `Server.Port`** を
使って MCP の URL を組み立てる。`--port 4322` だけで起動すると `http://localhost:4321/mcp/<id>` が書かれ、

- CLI が `Error: MCP tool mcp__agmux__permission_prompt ... not found` で **exit code 1** で落ちる
- 隔離環境のはずが本番 4321 にリクエストが飛ぶ

上記のとおり隔離 HOME の `~/.agmux/config.toml` に `[server] port = 4322` を書くこと。

### 罠2: 隔離 HOME だと Claude CLI が "Not logged in" になる

Keychain は HOME 非依存のはずだが、実際には隔離 HOME だと認証が通らない。隔離 HOME 側に
credentials を書き出して回避する（**後片付けで必ず消すこと**）:

```bash
mkdir -p /tmp/agmux-dev-home/.claude
security find-generic-password -w -s "Claude Code-credentials" > /tmp/agmux-dev-home/.claude/.credentials.json
```

### Pitfalls

- 隔離バックエンドの設定で、リモートブラウザのデバッグポートを本番と同じ値（既定の 9223）のままにしない。本番の Chrome に接続してしまう。接続先を変えるため、別のポート（例: 9333）を設定する。
- 起動直後に状態を一度でも問い合わせると接続が発生する。確認の前に、隔離用の設定でポートが本番と違うことを確かめる。
- agmux から起動した Chrome が、新規プロファイルではページを読み込まないことがある。その場合は、確認用に `--use-mock-keychain --password-store=basic` 付きで Chrome を手動で起動する。

### Tips: 長時間走る tool_use を作る

Claude Code は フォアグラウンドの `sleep` をハーネス側でブロックし、`run_in_background: false` を
指定した Bash も `system:task_started`(local_bash) としてタスク化することがある。
確実に長時間の tool_use を作るには CPU バウンドのワンライナーを使う:

```
python3 -c "print(sum(i*i for i in range(600000000)) % 1000007)"
```

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

### 罠: 一瞬だけ出る表示はスクショで捕まえにくい

- 読み込みが速いと、初回表示の一瞬（スケルトンや古い一覧）はスクショが間に合わず捕まらないことがある。
- そのような表示は、スクショに頼らず DOM の変化を記録して確かめるか、API の応答を遅らせて確かめる。
- 確認できなかった場合は、PR 本文に「未確認」と明記する。

映り込みに注意する:

- 一覧画面には、ほかのセッションの外部プロセス名などが映ることがある。uishot にアップロードする前にクロップし、映り込みを確認する。
- 添付後は、PR 本文やコメントの画像を開いて、映り込みがないか確かめる。

### Pitfalls
- 展開できる詳細を持つ行は、スマホ幅（約390px）でも確認する。ラベルが行の幅を使い切ると、展開部分が1文字幅に潰れることがある。
- 行は折り返せる（wrap する）構成にし、アイコンはラベルの span の中に入れる。アイコンだけが単独の行に落ちると、見た目が崩れる。
- 修正後は同じ幅でスクショを撮り直し、潰れと折り返しの崩れがないことを確かめてから PR に貼る。

## 4. 後片付け

```bash
lsof -ti :5173 | xargs kill        # Vite 停止
pkill -f "/tmp/agmux-dev"          # 隔離バックエンド停止（holder プロセスが残りやすいので -f で拾う）
agent-browser close                # ブラウザ終了
chmod -R +w /tmp/agmux-dev-home && rm -rf /tmp/agmux-dev-home /tmp/agmux-dev-tmp /tmp/agmux-dev
lsof -ti :4321 >/dev/null && echo "本番は無事"
```

`/tmp/agmux-dev-home/.claude/.credentials.json` を作った場合は、上の `rm -rf` に含まれていることを必ず確認すること。
