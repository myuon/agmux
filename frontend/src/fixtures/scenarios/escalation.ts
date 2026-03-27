// Auto-generated from real stream data
export const escalationLines = [
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": "escalateツールを使って、ユーザーに好きな色を質問してください。他のことはしないでください。"
    }
  },
  {
    "type": "system",
    "subtype": "init",
    "cwd": "/Users/user/ghq/github.com/myuon/agmux",
    "session_id": "00000000-0000-0000-0000-000000000002",
    "tools": [
      "Task",
      "TaskOutput",
      "Bash",
      "Glob",
      "Grep",
      "ExitPlanMode",
      "Read",
      "Edit",
      "Write",
      "NotebookEdit",
      "WebFetch",
      "TodoWrite",
      "WebSearch",
      "TaskStop",
      "AskUserQuestion",
      "Skill",
      "EnterPlanMode",
      "EnterWorktree",
      "CronCreate",
      "CronDelete",
      "CronList",
      "ToolSearch",
      "mcp__agmux__create_goal",
      "mcp__agmux__complete_goal",
      "mcp__agmux__escalate",
      "LSP"
    ],
    "mcp_servers": [
      {
        "name": "agmux",
        "status": "connected"
      },
      {
        "name": "claude.ai Google Calendar",
        "status": "needs-auth"
      },
      {
        "name": "claude.ai Gmail",
        "status": "needs-auth"
      },
      {
        "name": "claude.ai Sumyca",
        "status": "failed"
      }
    ],
    "model": "claude-opus-4-6",
    "permissionMode": "bypassPermissions",
    "slash_commands": [
      "keybindings-help",
      "debug",
      "simplify",
      "batch",
      "loop",
      "claude-api",
      "commit",
      "impl",
      "gh",
      "spec",
      "find-skills",
      "create-agent-skills",
      "ccskills",
      "commit",
      "agent-browser",
      "ralph-loop:help",
      "ralph-loop:cancel-ralph",
      "ralph-loop:ralph-loop",
      "compact",
      "context",
      "cost",
      "heapdump",
      "init",
      "pr-comments",
      "release-notes",
      "review",
      "security-review",
      "extra-usage",
      "insights"
    ],
    "apiKeySource": "none",
    "claude_code_version": "2.1.71",
    "output_style": "default",
    "agents": [
      "general-purpose",
      "statusline-setup",
      "Explore",
      "Plan"
    ],
    "skills": [
      "keybindings-help",
      "debug",
      "simplify",
      "batch",
      "loop",
      "claude-api",
      "commit",
      "impl",
      "gh",
      "spec",
      "find-skills",
      "create-agent-skills",
      "ccskills",
      "commit",
      "agent-browser"
    ],
    "plugins": [
      {
        "name": "rust-analyzer-lsp",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/rust-analyzer-lsp/1.0.0"
      },
      {
        "name": "ralph-loop",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/ralph-loop/205b6e0b3036"
      },
      {
        "name": "gopls-lsp",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/gopls-lsp/1.0.0"
      }
    ],
    "uuid": "00000000-0000-0000-0000-000000000000",
    "fast_mode_state": "off"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01WQcb7g8BS8xBdHFpjZLKSt",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "thinking",
          "thinking": "The user wants me to use the escalate tool to ask the user about their favorite color.",
          "signature": "Ev4BCkYICxgCKkB9X+ee+QpU0Qjt7nxb26gFjWvVZhV/+qKi/dsMMvdf2kaRRwFdocW56RVgD4wCzT0bleAZdY9NyDdpJ/WDzAQ/EgyXdrtztausVIXuUPEaDJktRqsWJD/GE7lrHCIwBMzOUZlCsOOVf2LaLrzqEWxXHv7QD0Wq3ldcdi2AmvSfCWFYt72NSxLLrbcQhbCBKma3RiHndh6GYCNi562Otq9fj7+6QONFhv4qvQA8eNCd4HVYGvyiE8RkC66RP1iPKyATpNBitHSOK6wfWT0p5onia4ZfiVZbczmxoUDZwnu6hHOyMy78aVFFJmRMGLvJ8r2WLeU/g+MYAQ=="
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 3,
        "cache_creation_input_tokens": 1908,
        "cache_read_input_tokens": 5213,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 1908
        },
        "output_tokens": 9,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000001"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01WQcb7g8BS8xBdHFpjZLKSt",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "toolu_01WiC4WCHiQdumBpE5giXKxD",
          "name": "ToolSearch",
          "input": {
            "query": "select:mcp__agmux__escalate",
            "max_results": 1
          },
          "caller": {
            "type": "direct"
          }
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 3,
        "cache_creation_input_tokens": 1908,
        "cache_read_input_tokens": 5213,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 1908
        },
        "output_tokens": 9,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000005"
  },
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "toolu_01WiC4WCHiQdumBpE5giXKxD",
          "content": [
            {
              "type": "tool_reference",
              "tool_name": "mcp__agmux__escalate"
            }
          ]
        }
      ]
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000004",
    "tool_use_result": {
      "matches": [
        "mcp__agmux__escalate"
      ],
      "query": "select:mcp__agmux__escalate",
      "total_deferred_tools": 25
    }
  },
  {
    "type": "rate_limit_event",
    "rate_limit_info": {
      "status": "allowed",
      "resetsAt": 1772892000,
      "rateLimitType": "five_hour",
      "overageStatus": "rejected",
      "overageDisabledReason": "out_of_credits",
      "isUsingOverage": false
    },
    "uuid": "00000000-0000-0000-0000-000000000007",
    "session_id": "00000000-0000-0000-0000-000000000002"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01SDWti6hAtXBQZzY27qDrbj",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "toolu_01PWD6dcuCk4Yewbztfn3Uxv",
          "name": "mcp__agmux__escalate",
          "input": {
            "message": "好きな色は何ですか？"
          },
          "caller": {
            "type": "direct"
          }
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 3,
        "cache_creation_input_tokens": 315,
        "cache_read_input_tokens": 7121,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 315
        },
        "output_tokens": 33,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000006"
  },
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": [
        {
          "tool_use_id": "toolu_01PWD6dcuCk4Yewbztfn3Uxv",
          "type": "tool_result",
          "content": [
            {
              "type": "text",
              "text": "User responded: あか"
            }
          ]
        }
      ]
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000009",
    "tool_use_result": [
      {
        "type": "text",
        "text": "User responded: あか"
      }
    ]
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01NZjVMp1zfkvdcR3CEffEp9",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "ユーザーの好きな色は**赤**とのことです。"
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 1,
        "cache_creation_input_tokens": 88,
        "cache_read_input_tokens": 7436,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 88
        },
        "output_tokens": 6,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000002",
    "uuid": "00000000-0000-0000-0000-000000000008"
  },
  {
    "type": "result",
    "subtype": "success",
    "is_error": false,
    "duration_ms": 16797,
    "duration_api_ms": 7806,
    "num_turns": 3,
    "result": "ユーザーの好きな色は**赤**とのことです。",
    "stop_reason": "end_turn",
    "session_id": "00000000-0000-0000-0000-000000000002",
    "total_cost_usd": 0.029588749999999997,
    "usage": {
      "input_tokens": 7,
      "cache_creation_input_tokens": 2311,
      "cache_read_input_tokens": 19770,
      "output_tokens": 209,
      "server_tool_use": {
        "web_search_requests": 0,
        "web_fetch_requests": 0
      },
      "service_tier": "standard",
      "cache_creation": {
        "ephemeral_1h_input_tokens": 2311,
        "ephemeral_5m_input_tokens": 0
      },
      "inference_geo": "",
      "iterations": [],
      "speed": "standard"
    },
    "modelUsage": {
      "claude-opus-4-6": {
        "inputTokens": 7,
        "outputTokens": 209,
        "cacheReadInputTokens": 19770,
        "cacheCreationInputTokens": 2311,
        "webSearchRequests": 0,
        "costUSD": 0.029588749999999997,
        "contextWindow": 200000,
        "maxOutputTokens": 32000
      }
    },
    "permission_denials": [],
    "fast_mode_state": "off",
    "uuid": "00000000-0000-0000-0000-000000000003"
  }
];
