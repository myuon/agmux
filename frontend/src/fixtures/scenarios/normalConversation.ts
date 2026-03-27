// Normal conversation with text + tool calls
export const normalConversationLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "frontend/src/App.tsx を読んで概要を教えてください。",
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "text",
          text: "App.tsxの内容を確認します。",
        },
        {
          type: "tool_use",
          id: "toolu_normal_demo_001",
          name: "Read",
          input: { file_path: "/Users/demo/project/frontend/src/App.tsx" },
        },
      ],
    },
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          tool_use_id: "toolu_normal_demo_001",
          type: "tool_result",
          content:
            '     1→import { useState } from "react";\n     2→import { Outlet } from "react-router-dom";\n     3→\n     4→function App() {\n     5→  return <Outlet />;\n     6→}\n     7→\n     8→export default App;\n',
        },
      ],
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "text",
          text: "App.tsx の概要は以下の通りです:\n\n- **React Router** の `Outlet` を使用したルーティング構成\n- `App` コンポーネントは `Outlet` をレンダリングするシンプルな構成\n- 子ルートのコンテンツがここに描画されます",
        },
      ],
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "tool_use",
          id: "toolu_normal_demo_002",
          name: "Grep",
          input: { pattern: "export.*function", path: "frontend/src/App.tsx" },
        },
      ],
    },
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          tool_use_id: "toolu_normal_demo_002",
          type: "tool_result",
          content: "Found 2 matches\n4: export function Dashboard() {\n42: export default App;",
        },
      ],
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "text",
          text: "`Dashboard` コンポーネントもエクスポートされています。これがメインページのセッション一覧を表示するコンポーネントです。",
        },
      ],
    },
  },
];
