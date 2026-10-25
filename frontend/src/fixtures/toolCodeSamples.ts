// Sample tool calls for previewing syntax highlighting in ToolCallView.
import type { StreamDisplayItem } from "../models/stream";

type ToolCallItem = Extract<StreamDisplayItem, { kind: "tool_call" }>;

export const codeToolSamples: { label: string; item: ToolCallItem }[] = [
  {
    "label": "Bash (シンタックスハイライト)",
    "item": {
      "kind": "tool_call",
      "name": "Bash",
      "input": {
        "command": "cd frontend && npx vite build 2>&1 | grep -E \"(dist|kB)\" | tail -5\nfor f in dist/assets/*.js; do echo \"$f: $(wc -c < \"$f\")\"; done",
        "description": "Build and list chunk sizes"
      },
      "result": "dist/assets/index-abc.js  612.34 kB\ndist/assets/tsx-def.js  178.20 kB",
      "toolUseId": "preview-bash-hl"
    }
  },
  {
    "label": "Read (途中から: 120-140行目)",
    "item": {
      "kind": "tool_call",
      "name": "Read",
      "input": {
        "file_path": "/Users/me/agmux/frontend/src/models/stream.ts",
        "offset": 120,
        "limit": 21
      },
      "result": "   120\t\n   121\t  // Pass 2: process task events\n   122\t  const tasks = new Map<string, ActiveTask>();\n   123\t  for (const entry of entries) {\n   124\t    const raw = entry as unknown as Record<string, unknown>;\n   125\t    if (entry.type !== \"system\") continue;\n   126\t    if (typeof raw.task_id === \"string\" && subagentOwnedTaskIds.has(raw.task_id)) continue;\n   127\t    const subtype = raw.subtype as string | undefined;\n   128\t    if (subtype === \"task_started\") {\n   129\t      const taskId = raw.task_id as string;\n   130\t      const taskType = raw.task_type as string;\n   131\t      const toolUseId = raw.tool_use_id as string | undefined;\n   132\t      if (taskId) {\n   133\t        const task: ActiveTask = { taskId, taskType: taskType || \"unknown\", toolCallHistory: [] };\n   134\t        if (raw.agent_id) task.agentId = raw.agent_id as string;\n   135\t        if (toolUseId) task.toolUseId = toolUseId;\n   136\t        if (raw.description) task.description = raw.description as string;\n   137\t        if (raw.timestamp) task.startedAt = raw.timestamp as string;\n   138\t        // Resolve tool input from the corresponding tool_use block\n   139\t        if (toolUseId && toolUseInputs.has(toolUseId)) {\n   140\t          const tu = toolUseInputs.get(toolUseId)!;",
      "toolUseId": "preview-read-hl"
    }
  },
  {
    "label": "Write (.go)",
    "item": {
      "kind": "tool_call",
      "name": "Write",
      "input": {
        "file_path": "/Users/me/app/main.go",
        "content": "package main\n\nimport (\n\t\"fmt\"\n\t\"net/http\"\n\t\"time\"\n)\n\n// Server serves health checks.\ntype Server struct {\n\taddr    string\n\tstarted time.Time\n}\n\nfunc (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {\n\tuptime := time.Since(s.started)\n\tfmt.Fprintf(w, \"ok (uptime=%s)\\n\", uptime.Round(time.Second))\n}\n\nfunc main() {\n\ts := &Server{addr: \":8080\", started: time.Now()}\n\thttp.HandleFunc(\"/health\", s.handleHealth)\n\tif err := http.ListenAndServe(s.addr, nil); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
      },
      "result": "File created successfully at: /Users/me/app/main.go",
      "toolUseId": "preview-write-hl"
    }
  },
  {
    "label": "Edit (.tsx, cat -n スニペット付き)",
    "item": {
      "kind": "tool_call",
      "name": "Edit",
      "input": {
        "file_path": "/src/components/ToolCallView.tsx",
        "old_string": "  return (\n    <pre className=\"text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap\">\n      {item.result.slice(0, 2000)}\n    </pre>\n  );",
        "new_string": "  // Highlight numbered results with their original line numbers\n  return (\n    <ToolResultView toolName={item.name} input={item.input} result={item.result} />\n  );",
        "replace_all": false
      },
      "result": "The file /src/components/ToolCallView.tsx has been updated. Here's the result of running `cat -n` on a snippet of the edited file:\n   412\texport function Output({ item }: Props) {\n   413\t  if (!item.result) return null;\n   414\t  // Highlight numbered results with their original line numbers\n   415\t  return (\n   416\t    <ToolResultView toolName={item.name} input={item.input} result={item.result} />\n   417\t  );\n   418\t}",
      "toolUseId": "preview-edit-hl"
    }
  }
];
