---
name: react-no-useeffect
description: React の useEffect を避け、より適切なパターンに置き換えるためのガイド。useEffect を含むコードを書こうとしているとき、またはコードレビューで useEffect を検出したとき、代替パターンを提案するために使う。
user-invocable: false
---

# React: useEffect を使わない設計パターン

参考: [You Might Not Need an Effect](https://react.dev/learn/you-might-not-need-an-effect)

## useEffect の代替パターン一覧

| ケース | NG (useEffect) | OK (代替) |
|--------|---------------|-----------|
| データフェッチ | useEffect + useState | loader / SWR / React Query / use() |
| 派生状態 | useEffect で state を同期 | useMemo / レンダー中の計算 |
| イベントへの反応 | useEffect で変更を検知 | イベントハンドラ内で直接処理 |
| 外部ストアとの同期 | useEffect + subscribe | useSyncExternalStore |
| props/state リセット | useEffect で key 変更を検知 | key prop でコンポーネントを再マウント |
| 親への通知 | useEffect で親の setState | イベントハンドラ内で親のコールバックを呼ぶ |

## パターン別コード例

### データフェッチ

```tsx
// NG
function Profile({ id }: { id: string }) {
  const [data, setData] = useState(null);
  useEffect(() => {
    fetchUser(id).then(setData);
  }, [id]);
  return <div>{data?.name}</div>;
}

// OK: React Query
function Profile({ id }: { id: string }) {
  const { data } = useQuery({ queryKey: ["user", id], queryFn: () => fetchUser(id) });
  return <div>{data?.name}</div>;
}

// OK: React Router loader
export async function loader({ params }: { params: { id: string } }) {
  return fetchUser(params.id);
}
function Profile() {
  const data = useLoaderData();
  return <div>{data?.name}</div>;
}
```

### 派生状態

```tsx
// NG
function TodoList({ todos, filter }: { todos: Todo[]; filter: string }) {
  const [filtered, setFiltered] = useState(todos);
  useEffect(() => {
    setFiltered(todos.filter((t) => t.status === filter));
  }, [todos, filter]);
  return <ul>{filtered.map(/*...*/)}</ul>;
}

// OK: レンダー中に計算
function TodoList({ todos, filter }: { todos: Todo[]; filter: string }) {
  const filtered = todos.filter((t) => t.status === filter);
  return <ul>{filtered.map(/*...*/)}</ul>;
}

// OK: 高コストなら useMemo
function TodoList({ todos, filter }: { todos: Todo[]; filter: string }) {
  const filtered = useMemo(() => todos.filter((t) => t.status === filter), [todos, filter]);
  return <ul>{filtered.map(/*...*/)}</ul>;
}
```

### イベントへの反応

```tsx
// NG
function Form() {
  const [submitted, setSubmitted] = useState(false);
  useEffect(() => {
    if (submitted) {
      showToast("送信完了");
    }
  }, [submitted]);
  return <button onClick={() => setSubmitted(true)}>送信</button>;
}

// OK: イベントハンドラ内で処理
function Form() {
  const handleSubmit = () => {
    submitForm();
    showToast("送信完了");
  };
  return <button onClick={handleSubmit}>送信</button>;
}
```

### 外部ストアとの同期

```tsx
// NG
function WindowWidth() {
  const [width, setWidth] = useState(window.innerWidth);
  useEffect(() => {
    const handler = () => setWidth(window.innerWidth);
    window.addEventListener("resize", handler);
    return () => window.removeEventListener("resize", handler);
  }, []);
  return <div>{width}</div>;
}

// OK: useSyncExternalStore
function WindowWidth() {
  const width = useSyncExternalStore(
    (cb) => { window.addEventListener("resize", cb); return () => window.removeEventListener("resize", cb); },
    () => window.innerWidth,
  );
  return <div>{width}</div>;
}
```

### props 変更による state リセット

```tsx
// NG
function Chat({ roomId }: { roomId: string }) {
  const [messages, setMessages] = useState<Message[]>([]);
  useEffect(() => {
    setMessages([]);
  }, [roomId]);
  return <MessageList messages={messages} />;
}

// OK: key prop で再マウント
function ChatPage({ roomId }: { roomId: string }) {
  return <Chat key={roomId} roomId={roomId} />;
}
```

## useEffect が必要なケース

以下は useEffect を使うべき正当なケース:

- **DOM 操作**: ref 経由での focus、scroll、要素サイズ計測
- **外部ライブラリ連携**: D3、地図ライブラリ等の初期化・クリーンアップ
- **アニメーション**: requestAnimationFrame によるアニメーション制御
- **WebSocket / SSE**: サーバープッシュ接続の確立・切断

## コードレビュー時の判断基準

useEffect を見つけたら以下を確認する:

1. **setState を呼んでいるか?** → 派生状態かイベント処理で置き換え可能な可能性が高い
2. **データフェッチしているか?** → React Query / SWR / loader に置き換える
3. **subscribe/addEventListener しているか?** → useSyncExternalStore を検討
4. **deps に props/state があり、別の state を更新しているか?** → 不要な useEffect の典型パターン
