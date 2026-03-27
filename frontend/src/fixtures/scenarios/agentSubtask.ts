// Agent subtask (sub-agent task_progress) scenario
export const agentSubtaskLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "ToolCallViewコンポーネントのコードを調査してください。",
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "tool_use",
          id: "toolu_agent_demo_001",
          name: "Bash",
          input: { command: "grep -r 'ToolCallView' frontend/src/", description: "Search for ToolCallView references" },
        },
      ],
    },
    parent_tool_use_id: null,
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          tool_use_id: "toolu_agent_demo_001",
          type: "tool_result",
          content: "frontend/src/components/session/ToolCallView.tsx\nfrontend/src/components/session/StreamOutputView.tsx",
        },
      ],
    },
  },
  {
    type: "system",
    subtype: "task_progress",
    task_id: "acd50d423fa43644f",
    tool_use_id: "toolu_agent_demo_001",
    description: "Reading frontend/src/components/session/ToolCallView.tsx",
    usage: { total_tokens: 58975, tool_uses: 18, duration_ms: 27015 },
    last_tool_name: "Read",
  },
  {
    type: "system",
    subtype: "task_progress",
    task_id: "acd50d423fa43644f",
    tool_use_id: "toolu_agent_demo_001",
    description: "Searching for toolIcon|toolDescription|parseTodoInput",
    usage: { total_tokens: 63570, tool_uses: 19, duration_ms: 29885 },
    last_tool_name: "Grep",
  },
  {
    type: "system",
    subtype: "task_progress",
    task_id: "acd50d423fa43644f",
    tool_use_id: "toolu_agent_demo_001",
    description: "Reading frontend/src/models/tool.ts",
    usage: { total_tokens: 65336, tool_uses: 21, duration_ms: 34675 },
    last_tool_name: "Read",
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "text",
          text: "ToolCallViewコンポーネントの調査が完了しました。主要な機能は以下の通りです:\n\n- **EscalateCallView**: エスカレーションツール呼び出しの表示\n- **PermissionPromptCallView**: パーミッション要求の表示\n- **TodoCallView**: Todoリストの表示\n- **AskUserQuestionCallView**: ユーザーへの質問表示",
        },
      ],
    },
  },
];
