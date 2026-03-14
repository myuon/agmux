---
name: react-router-loader
description: React Router v7 の loader パターンのリファレンス。画面表示に伴うデータフェッチに loader を使うことでデータの流れを一方向にし、コードの見通しを良くし、不要な useEffect の利用を抑制する。
user-invocable: false
---

# React Router Loader

画面表示に伴うデータフェッチには loader パターンを使う。データの流れを一方向（loader → コンポーネント）にすることで、コードの見通しが良くなり、不要な useEffect を排除できる。

## モードの判別方法

React Router v7 には 3 つのモードがあり、loader の書き方が異なる。

| 判別ポイント | Framework Mode | Data Mode | Declarative Mode |
|---|---|---|---|
| `vite.config.ts` に `reactRouter()` プラグイン | ✅ | — | — |
| `createBrowserRouter` を使用 | — | ✅ | — |
| `<BrowserRouter>` を使用 | — | — | ✅ |

Declarative Mode では loader は使えない。

## Data Mode

`createBrowserRouter` を使うモード。Vite プラグイン不要。

```tsx
import {
  createBrowserRouter,
  RouterProvider,
  useLoaderData,
} from "react-router";

interface User {
  id: string;
  name: string;
}

const router = createBrowserRouter([
  {
    path: "/users/:id",
    Component: UserPage,
    loader: async ({ params }): Promise<User> => {
      const res = await fetch(`/api/users/${params.id}`);
      if (!res.ok) throw new Response("Not Found", { status: 404 });
      return res.json();
    },
  },
]);

function UserPage() {
  const user = useLoaderData<User>();
  return <div>{user.name}</div>;
}

// エントリポイント
ReactDOM.createRoot(root).render(<RouterProvider router={router} />);
```

## Framework Mode

`@react-router/dev` の Vite プラグインを使うモード。ファイルベースルーティング・型安全な loader。

`./+types/` ディレクトリに自動生成される型を使うことで、loader の戻り値がコンポーネントの props に型安全に渡される。

```tsx
// app/routes/users.tsx
import type { Route } from "./+types/users";

// Route.LoaderArgs で params, request の型が付く
export async function loader({ params, request }: Route.LoaderArgs) {
  const user = await db.user.findUnique({ where: { id: params.id } });
  if (!user) {
    throw new Response("Not Found", { status: 404 });
  }
  return { user };
}

// Route.ComponentProps により loaderData の型が loader の戻り値から自動推論される
export default function Users({ loaderData }: Route.ComponentProps) {
  const { user } = loaderData;
  return <div>{user.name}</div>;
}
```

## エラーハンドリング

loader で throw した Response は ErrorBoundary でキャッチされる。

```tsx
export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  if (isRouteErrorResponse(error)) {
    return <div>{error.status}: {error.data}</div>;
  }
  return <div>Unexpected error</div>;
}
```

## request からの情報取得

```tsx
export async function loader({ request }: Route.LoaderArgs) {
  const url = new URL(request.url);
  const q = url.searchParams.get("q");           // クエリパラメータ
  const cookie = request.headers.get("Cookie");   // ヘッダー
  return { results: await search(q) };
}
```

## 参考リンク

- [React Router 公式ドキュメント](https://reactrouter.com/)
- [Route Module - loader](https://reactrouter.com/start/framework/route-module#loader)
- [Picking a Router](https://reactrouter.com/start/library/routing)
