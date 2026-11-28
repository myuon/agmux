// Tool errors wrapped in <tool_use_error> by Claude Code (based on real stream data)
export const toolUseErrorLines = [
  {
    type: "user",
    message: { role: "user", content: "ログの末尾を確認して" },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [{ type: "tool_use", id: "toolu_err_1", name: "bash", input: { command: "tail -n 2 sa8r.log" } }],
    },
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          type: "tool_result",
          tool_use_id: "toolu_err_1",
          is_error: true,
          content: "<tool_use_error>Error: No such tool available: bash</tool_use_error>",
        },
      ],
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [{ type: "tool_use", id: "toolu_err_2", name: "Bash", input: { command: "sleep 118 && tail -n 2 sa8r.log" } }],
    },
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          type: "tool_result",
          tool_use_id: "toolu_err_2",
          is_error: true,
          content:
            "<tool_use_error>Blocked: sleep 118 followed by: tail -n 2 sa8r.log. To wait for a condition, use Monitor with an until-loop (e.g. `until <check>; do sleep 2; done`). To wait for a command you started, use run_in_background: true.</tool_use_error>",
        },
      ],
    },
  },
];
