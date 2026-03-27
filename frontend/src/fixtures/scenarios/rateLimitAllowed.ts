// Auto-generated from real stream data
export const rateLimitAllowedLines = [
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": "EnterPlanMode ツールを呼び出してください。その後すぐに ExitPlanMode ツールを呼び出してください。"
    }
  },
  {
    "type": "system",
    "subtype": "init",
    "cwd": "/private/tmp/planmode-test",
    "session_id": "00000000-0000-0000-0000-000000000010",
    "tools": [
      "Task",
      "AskUserQuestion",
      "Bash",
      "CronCreate",
      "CronDelete",
      "CronList",
      "Edit",
      "EnterPlanMode",
      "EnterWorktree",
      "ExitPlanMode",
      "ExitWorktree",
      "Glob",
      "Grep",
      "LSP",
      "mcp__agmux__complete_goal",
      "mcp__agmux__create_goal",
      "mcp__agmux__escalate",
      "mcp__agmux__get_goal",
      "mcp__agmux__send_notification",
      "NotebookEdit",
      "Read",
      "RemoteTrigger",
      "Skill",
      "TaskOutput",
      "TaskStop",
      "TodoWrite",
      "ToolSearch",
      "WebFetch",
      "WebSearch",
      "Write"
    ],
    "mcp_servers": [
      {
        "name": "agmux",
        "status": "connected"
      },
      {
        "name": "claude.ai Sumyca",
        "status": "failed"
      },
      {
        "name": "claude.ai Google Calendar",
        "status": "needs-auth"
      },
      {
        "name": "claude.ai Gmail",
        "status": "needs-auth"
      }
    ],
    "model": "claude-haiku-4-5-20251001",
    "permissionMode": "bypassPermissions",
    "slash_commands": [
      "update-config",
      "debug",
      "simplify",
      "batch",
      "loop",
      "schedule",
      "claude-api",
      "commit",
      "impl",
      "gh",
      "dispatch",
      "spec",
      "find-skills",
      "create-agent-skills",
      "ccskills",
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
    "claude_code_version": "2.1.83",
    "output_style": "default",
    "agents": [
      "general-purpose",
      "statusline-setup",
      "Explore",
      "Plan"
    ],
    "skills": [
      "update-config",
      "debug",
      "simplify",
      "batch",
      "loop",
      "schedule",
      "claude-api",
      "commit",
      "impl",
      "gh",
      "dispatch",
      "spec",
      "find-skills",
      "create-agent-skills",
      "ccskills"
    ],
    "plugins": [
      {
        "name": "rust-analyzer-lsp",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/rust-analyzer-lsp/1.0.0"
      },
      {
        "name": "ralph-loop",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/ralph-loop/unknown"
      },
      {
        "name": "gopls-lsp",
        "path": "/Users/user/.claude/plugins/cache/claude-plugins-official/gopls-lsp/1.0.0"
      }
    ],
    "uuid": "00000000-0000-0000-0000-000000000001",
    "fast_mode_state": "off"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_01Gnq51vQAUkbRDU5Brvzeys",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "thinking",
          "thinking": "The user is asking me to call EnterPlanMode and then immediately call ExitPlanMode. This seems like a test or experiment with these tools.\n\nLet me check what these tools do:\n- EnterPlanMode: Used when about to start a non-trivial implementation task to get user sign-off on approach before writing code. It transitions to plan mode for exploring the codebase and designing an implementation approach.\n- ExitPlanMode: Used when the plan is complete and ready for user approval. It reads the plan from a file and signals that the user should review and approve it.\n\nThe user wants me to enter plan mode and then immediately exit it. However, ExitPlanMode requires that a plan has been written to a plan file. If I just call ExitPlanMode without any plan content, it will likely fail or be invalid.\n\nLooking at the ExitPlanMode description: \"You should have already written your plan to the plan file specified in the plan mode system message\" - but I haven't entered plan mode yet, so there's no plan file.\n\nI think the user is testing the behavior of these tools. Let me try to call EnterPlanMode first and see what happens.",
          "signature": "EqkKCmMIDBgCKkAJ/LXHHr0Wd4w9uZAf5CJi7vo6wv0eJMpUmR2ULsG3Cz+VZtzZWri5yRpiRQH9/er+0cBH6iWoS0byXL8dMV3HMhljbGF1ZGUtaGFpa3UtNC01LTIwMjUxMDAxOAASDOCiYieZyUEgRoGJtBoMJ9zraz5c3rrB+XlHIjClh9HcrzSZtx5dhQJZ1P0tnryeF44CtNGy3+H2203vLlKqIIqDm7Ug/HUd6+D2eOUq8wj39UWdRImgb2DTqZESv0pR6Pd2Usy5H9wVTDqbPdfirlNtsGrhIGYPeeEROH5rLst6WQ4GOr/imH8hK4GA+EXCW41ik+5km48QrFez6/oVrEsFhhW8mMLUgMuIPfdJakSqFz3grdljgXQFQWR6slMq05DSNV5Ht7WFRJ3BqsaegkOSAlDwMwo+ZwlZu+BkTfT/8sQrN2YhiET0glfy5wjrcmQSvv9UZbiwb1J/WZ8pTZC/Ay4ZFmNWsqOIG4thXZVaik+W9IQsoZO8PsBE52BakZeLsc4G8k3PDJBVRr4KBC0qdtheaPJHVduvyrylvHMVku/V7XCtY1C6Yk9NU4/gOUP6Hyljb8iNMzBemmwN1V9gz3HFUDLlp+Il9pT/7LNGJ8uEHfOdHv8m0CUHq/Fx/ZJei3KD9xGDs6rBLIqvLrgcremQ6s1AHnIkKMQIHqNdI+jv5bGGhgvKCAJAcLIoxMAUrLuHE/hAl4OtySwNumMQ5ONNreSd4b6KyYL+RHbpRKxmnsWQSCSuy6RvUI6wFZ8TWymN84AM3QCNligMMlawUGjo1RJE5VJhcGQqJb8RJS5LbO4MP0K/5qLfiiw2KxWLCxe5Ni2veCNY9YbZj1r8WXvrQDSt1UHLKZn6k4oP+p8kVRDUaH/ev2NzBY+ScUU/huQNKo16UTZcGYvxxQq1UdBMSiHf4EOAwitA8ULSBzlnbs6Y4+GkQgd/IqEQrZyBGB/XBmFlaTH3an2YYdd4e6/TfHnKCVJGa8i45yxxeSqYKxJF5fzv05ZS9q0noCnodOq/+PmcFfF2SBveKEhYIscdewEMkZhFCSRRCdAc4D7Vn0b7BogMpcWzcslmLCnmf3YaX+Qp5iVvk2BZIC7R6D7Rv7ySfWMk9aGjxg37Ly/R70x/QSNeMWU/7tNb4V2v8YdS/WMz6nTAvGptK6wA9NUMLaXoc+ENlS09+Bx7QFGNKZt3esU0bj0vpTG6ITL8we2i8Zwb8CvMweZyXw1COUt5VhgWmAlvv6h+kyVaFABOU5KCLKxb2MH1koBvuTCaW33JlM4nl1e0Aun4IrDr04oeE+LqNY/U3Co+0jV+2zVDTteDg2PEY0TbBwKyf1ISbb77aI1/8o2s/QIZ9sf9eSu+nN2X5kXQLghucS6k6DZAW6blfSvdMWXz+kfMn54BX/Em1TnOYOsW9kwIRmZMYgZUkVFlmfVDNubRvsR4PJP01emLX57Oxjh5i51cvOeTAkpfYZyizmMnU9pNNlX/Ze5IJcBdOI3xjkQk265QMs+zkgLPADIUgIFzJRDMGNIA5ZyFhaxSCru6nLesi3HXZQwxLklThwdZItCbdXTc8+YgJuPmHpYsbqhokdtY1KWCAgL96KvXFq5lWyalEEXbcn1Acka+8hGA0v+RO/bpuJXvfQQpUh8MyoizGqHVGO0u7QQ04LaO321l/0XIWjRGzKTlAbXDDMzs32+pTwNGSt4iGUCHXSXjXNlLDNBZGmpDYUeBMB7ngD1UuaK4XpOW1RgB"
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 10,
        "cache_creation_input_tokens": 22637,
        "cache_read_input_tokens": 7853,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 22637
        },
        "output_tokens": 8,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000011"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_01Gnq51vQAUkbRDU5Brvzeys",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "toolu_01CnrNSuVMKiS3Tn5hecZNot",
          "name": "EnterPlanMode",
          "input": {},
          "caller": {
            "type": "direct"
          }
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 10,
        "cache_creation_input_tokens": 22637,
        "cache_read_input_tokens": 7853,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 22637
        },
        "output_tokens": 8,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000003"
  },
  {
    "type": "system",
    "subtype": "status",
    "status": null,
    "permissionMode": "plan",
    "uuid": "00000000-0000-0000-0000-000000000007",
    "session_id": "00000000-0000-0000-0000-000000000010"
  },
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "content": "Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.\n\nIn plan mode, you should:\n1. Thoroughly explore the codebase to understand existing patterns\n2. Identify similar features and architectural approaches\n3. Consider multiple approaches and their trade-offs\n4. Use AskUserQuestion if you need to clarify the approach\n5. Design a concrete implementation strategy\n6. When ready, use ExitPlanMode to present your plan for approval\n\nRemember: DO NOT write or edit any files yet. This is a read-only exploration and planning phase.",
          "tool_use_id": "toolu_01CnrNSuVMKiS3Tn5hecZNot"
        }
      ]
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000009",
    "timestamp": "2026-03-27T02:13:22.193Z",
    "tool_use_result": {
      "message": "Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach."
    }
  },
  {
    "type": "rate_limit_event",
    "rate_limit_info": {
      "status": "allowed_warning",
      "resetsAt": 1774587600,
      "rateLimitType": "seven_day",
      "utilization": 0.9,
      "isUsingOverage": false,
      "surpassedThreshold": 0.75
    },
    "uuid": "00000000-0000-0000-0000-000000000000",
    "session_id": "00000000-0000-0000-0000-000000000010"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_01F5kRuaNjviZAUCUq28wVzs",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "thinking",
          "thinking": "I've entered plan mode. Now the user wants me to immediately call ExitPlanMode. However, I haven't created any plan yet. The system message says \"No plan file exists yet\" and I should create one at `/Users/user/.claude/plans/ethereal-growing-mochi.md`.\n\nSince the user is asking me to immediately exit, I should call ExitPlanMode right now. But ExitPlanMode expects that I have written a plan to the plan file. Let me call it and see what happens.",
          "signature": "EocFCmMIDBgCKkDLN7X5hsvpQgehxqp+nHTNG5rU20Bp9C9+iKMJBmPqTiF0zd08cauvb6YQA0iqfhaSvaxNK/U8WJX2Q07IUkX8MhljbGF1ZGUtaGFpa3UtNC01LTIwMjUxMDAxOAASDBQycYbN8aaO/rEsVxoMMA/tfM6hzZgKqppYIjAx3aY+gf3VodbYuXUzsNSoQFQ8AiDFaPqgRBMHxzE++t7J4LHjvkbnu7W4o/gsrf8q0QNDO/4W7+cw+aChayCLAb3mN+fMBv1KTqNBAgDxECMb4LzKzuB8PDKOkg3T7eR2VQjLJ1qVnUzi+VljXaPkfv0LobK9uv9M/U5A/shrgf9QBiRUOrsUgJkRPLNVkxotl7LTMcr2s1sCfqtKtm1WbzedBMPcpx5UpsUByQQ3kG7R0fTCJLWmUxtYnMUn4uyhg6hkgLu8n0x2kChvZcYThm0nZDC20/zclmWRl9xhRgxgo4cidOMzVdusK1OTISME/WW49SqUJOvPvtFXqOgxuTYxIOdzVOlMYR0x0yXUzbmuSfKDE46p0kdAceIs/hw/ZQyPpE6Dm3WQQd/HN6O/lr4VrKASuhJyBPHq+UVWBH/1oN0xxaSUxB1m+TgKodlISQ1d2/Vebe7SAN127utdtGakN4JAS05C53eUkChPp506BMa7qa4XuMZ0K29Se1vMPOxeEeW+XzdN5IF04TAi3Z50Q4CoC5kC8wqzDaK3rC7374jvpVYnJ+nuzoq634Z0JlCLN1cGgJtpIpkaTrpepu1zLNuzwhmY+ibpXVU0/zO/Va5scXGpx4fygtu2gxkZF3Qr91DjpGEr+12wagVTTLRlfLSkHWQmAoN8DyXlwOy9rrsYAQ=="
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 8,
        "cache_creation_input_tokens": 1651,
        "cache_read_input_tokens": 30490,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 1651
        },
        "output_tokens": 3,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000004"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_01F5kRuaNjviZAUCUq28wVzs",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "toolu_017iGSV4ogDHE8T87yQ4vPy5",
          "name": "ExitPlanMode",
          "input": {},
          "caller": {
            "type": "direct"
          }
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 8,
        "cache_creation_input_tokens": 1651,
        "cache_read_input_tokens": 30490,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 1651
        },
        "output_tokens": 3,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000002"
  },
  {
    "type": "user",
    "message": {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "content": "Exit plan mode?",
          "is_error": true,
          "tool_use_id": "toolu_017iGSV4ogDHE8T87yQ4vPy5"
        }
      ]
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000006",
    "timestamp": "2026-03-27T02:13:24.768Z",
    "tool_use_result": "Error: Exit plan mode?"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_014XheniHhpGYd6iMiaru9yn",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "thinking",
          "thinking": "It looks like ExitPlanMode asked for confirmation, perhaps because no plan file was written. This is the expected behavior when trying to exit plan mode without a plan. The user's request was to call EnterPlanMode and then immediately call ExitPlanMode - which I've done, and ExitPlanMode is prompting for confirmation since there's no plan.\n\nThis appears to be a test of the plan mode workflow. Let me complete the flow by confirming the exit (if that's what's expected).",
          "signature": "Ep4FCmMIDBgCKkCjkxNw+GJtGEImgZYoKWJmBC0VPYqt8FemWUBu3Xbw59KYUPSiEcROun5jgW+mPMwGsB4nqpz2T9bQI8WBraL4MhljbGF1ZGUtaGFpa3UtNC01LTIwMjUxMDAxOAASDMzl1Fc1PPs5aLBPqBoMvWICZfyyiQUfhDLSIjDBdD1K1hEMAr62Z+XPl1RsutlSnigPy5IYLP6h7yFQpAolDxedMbDRcfSkGD+C54Qq6ANKgB5MbKeKldGnPyxHQOtQhJsgai6399OmHOPJLqp8G1TZSLgwd2h629hXs71LRrHmiHwLFFsBW0uRJ63hq2ASyG35r9n6crhjGUPHmQqB1/mrtBjbV3/fDmdXwq4y9VMJgBkAq7HJjht0j7R4JBEElOgDE8vfttK2nwbOnrcBfSps18GdAKZbB4LXGZbCvKIL4HIqkAUlPiYoj0agr1SwJBJ7sgbowLZzmOG6YKiMTnyzyLpabkJWlERPRZ1Ky7Oc0V8tBPJWvfl9Lx8gYbZL7B9ZGdBsSMLHHLuD47odongXLeYbHXvnFLzhpCS0Wb0XSfhJJxH7YzhXbHbMCAcQmHfnG0x1YPugh9Zs7VGrIm8AHffOGjtsn8j4ASN+oT66adjMGyE+xk5J6/3oA2FruytgmvyBgqTNppLXxZ4v+8RRzGMskzCtWMkyIvTuLZ+WOygEH6txizph6+UIDfrR3JLI2b0tNExYD+ugYrLoMoFlNbfnZK46JfrCN11sQ69Lj59QFv0vPNpvZT7YNT/BsoYx0mlpb2jxSqgHHsrEDQ1K8sdetgiR9co0XTl8Qlt49yNvH4Ycf2BNMxhA2rxgpRllpIXMnewCKdoFN2OHMTmNuxgsn7B+CrqaTDRbVOFvqpoCBaZQFRgB"
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 8,
        "cache_creation_input_tokens": 195,
        "cache_read_input_tokens": 32141,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 195
        },
        "output_tokens": 1,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000008"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-haiku-4-5-20251001",
      "id": "msg_014XheniHhpGYd6iMiaru9yn",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "I've called both tools as requested. EnterPlanMode is now active, and ExitPlanMode is prompting for confirmation since no plan file was created. This appears to be testing the plan mode workflow—did you want me to proceed differently, or was this just a verification of the tools?"
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 8,
        "cache_creation_input_tokens": 195,
        "cache_read_input_tokens": 32141,
        "cache_creation": {
          "ephemeral_5m_input_tokens": 0,
          "ephemeral_1h_input_tokens": 195
        },
        "output_tokens": 1,
        "service_tier": "standard",
        "inference_geo": "not_available"
      },
      "context_management": null
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000010",
    "uuid": "00000000-0000-0000-0000-000000000005"
  }
];
