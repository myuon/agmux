// Escalation tool call scenario
export const escalationLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "escalateツールを使って、ユーザーに好きな色を質問してください。",
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "tool_use",
          id: "toolu_escalate_demo_001",
          name: "mcp__agmux__escalate",
          input: { message: "好きな色は何ですか？" },
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
          tool_use_id: "toolu_escalate_demo_001",
          type: "tool_result",
          content: [{ type: "text", text: "User responded: あか" }],
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
          text: "ユーザーの好きな色は**赤**とのことです。",
        },
      ],
    },
  },
];
