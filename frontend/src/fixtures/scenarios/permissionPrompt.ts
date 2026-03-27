// Permission prompt scenario
export const permissionPromptLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "ファイルを削除してください。",
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "tool_use",
          id: "toolu_permission_demo_001",
          name: "mcp__agmux__permission_prompt",
          input: {
            tool_name: "Bash",
            input: { command: "rm -rf /tmp/build", description: "Delete build directory" },
          },
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
          tool_use_id: "toolu_permission_demo_001",
          type: "tool_result",
          content: [{ type: "text", text: "Permission granted by user" }],
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
          id: "toolu_bash_demo_001",
          name: "Bash",
          input: { command: "rm -rf /tmp/build", description: "Delete build directory" },
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
          tool_use_id: "toolu_bash_demo_001",
          type: "tool_result",
          content: "Directory deleted successfully.",
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
          text: "/tmp/build ディレクトリを削除しました。",
        },
      ],
    },
  },
];
