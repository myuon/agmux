# Stage 2: HTTP API + Web UI基盤

## 目的

REST APIサーバーとReact Web UIの骨格を作り、ブラウザからセッションのCRUD操作ができるようにする。
このステージ完了時点で `agmux serve` → ブラウザでセッション作成・一覧表示が動く。

## 成果物

- chi HTTPサーバー + REST API
- React + Vite フロントエンド
- Go embedによるsingle binary同梱
- セッション一覧・作成のUI

## タスク

### 2.1 HTTPサーバー (`internal/server/server.go`)

```go
type Server struct {
    session *session.Manager
    router  chi.Router
}
```

REST APIエンドポイント:

```
GET    /api/sessions          → セッション一覧
POST   /api/sessions          → セッション作成 { name, projectPath, prompt }
GET    /api/sessions/:id      → セッション詳細
DELETE /api/sessions/:id      → セッション削除
POST   /api/sessions/:id/stop → セッション停止
POST   /api/sessions/:id/send → テキスト送信 { text }
GET    /api/sessions/:id/output → 画面キャプチャ取得
```

- レスポンス形式: JSON
- エラーハンドリング: `{ "error": "message" }` 形式
- CORSは開発時のみ許可（Vite devサーバー用）

### 2.2 フロントエンド初期化 (`frontend/`)

```
frontend/
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts
├── src/
│   ├── main.tsx
│   ├── App.tsx
│   ├── api/
│   │   └── client.ts         # fetch wrapper
│   ├── components/
│   │   ├── SessionList.tsx    # セッション一覧
│   │   ├── SessionCard.tsx    # 個別セッションカード
│   │   ├── CreateSession.tsx  # 新規作成フォーム
│   │   └── Layout.tsx         # レイアウト
│   └── types/
│       └── session.ts         # 型定義
```

- パッケージマネージャー: npm
- UIライブラリ: 最小限のCSS（Tailwind CSS）
- 状態管理: React hooks（useState/useEffect）で十分

### 2.3 ダッシュボード画面

**セッション一覧ページ（メイン画面）:**

```
┌─────────────────────────────────────────────┐
│  agmux Dashboard                    [+ New] │
├─────────────────────────────────────────────┤
│ ┌─────────────────┐ ┌─────────────────┐     │
│ │ session-1       │ │ session-2       │     │
│ │ 🟢 running      │ │ 🟡 waiting      │     │
│ │ /path/to/proj1  │ │ /path/to/proj2  │     │
│ │ 2min ago        │ │ 5min ago        │     │
│ │ [Stop] [View]   │ │ [Stop] [View]   │     │
│ └─────────────────┘ └─────────────────┘     │
└─────────────────────────────────────────────┘
```

**新規作成モーダル:**
- セッション名（テキスト）
- プロジェクトパス（テキスト）
- 初期プロンプト（テキストエリア、任意）

### 2.4 Go embedによるフロントエンド同梱

```go
//go:embed frontend/dist/*
var frontendFS embed.FS

// chi routerでSPAのフォールバック設定
// /api/* → APIハンドラー
// それ以外 → frontendFS
```

- ビルドスクリプト: `Makefile` に `build-frontend` + `build` を定義
- `make build` で frontend build → go build の順で実行

### 2.5 `agmux serve` コマンド

```
agmux serve [-p port] [--dev]
```

- デフォルトポート: 4321
- `--dev` フラグ: CORSを許可し、フロントエンドはVite devサーバーに委譲
- 起動時にSQLiteを初期化し、HTTPサーバーを開始

## 完了条件

- [ ] `agmux serve` でHTTPサーバーが起動する
- [ ] `GET /api/sessions` でJSON応答が返る
- [ ] `POST /api/sessions` でセッションが作成される
- [ ] ブラウザでダッシュボードが表示される
- [ ] UIからセッション作成・一覧表示・停止ができる
- [ ] `make build` でsingle binaryがビルドされる
- [ ] ビルドしたバイナリ単体でフロントエンドが配信される
