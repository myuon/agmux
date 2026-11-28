// Large tool output wrapped in <persisted-output> by Claude Code (based on real stream data)
export const persistedOutputLines = [
  {
    type: "user",
    message: { role: "user", content: "実験ログをまとめて見せて" },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [{ type: "tool_use", id: "toolu_persist_1", name: "Bash", input: { command: "cat experiments/*/rounds.jsonl" } }],
    },
  },
  {
    type: "user",
    message: {
      role: "user",
      content: [
        {
          type: "tool_result",
          tool_use_id: "toolu_persist_1",
          content:
            "<persisted-output>\nOutput too large (34.2KB). Full output saved to: /Users/example/.claude/projects/example/tool-results/b0rfhxob9.txt\n\nPreview (first 2KB):\n{\"kind\": \"reliability\", \"n\": 0, \"split\": \"w20261117T11Z\"}\n{\"kind\": \"adopt\", \"candidate\": \"rg2_outhalf\", \"n_trials\": 13}\n...\n</persisted-output>\nShell cwd was reset to /Users/example/repo",
        },
      ],
    },
  },
];
