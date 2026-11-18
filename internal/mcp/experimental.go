package mcp

import "github.com/myuon/agmux/internal/experimental"

// experimentalTools maps an MCP tool name to the experimental feature that
// gates it. Gated tools are hidden from tools/list and rejected on call while
// the feature is off.
var experimentalTools = map[string]string{
	"search_sessions":    "session_search",
	"loop_task_add":      "loops",
	"loop_task_next":     "loops",
	"loop_task_done":     "loops",
	"loop_report":        "loops",
	"get_project_status": "projects",
}

func toolAvailable(name string) bool {
	feature, gated := experimentalTools[name]
	return !gated || experimental.Enabled(feature)
}

func filterExperimentalTools(tools []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tools))
	for _, t := range tools {
		if m, ok := t.(map[string]interface{}); ok {
			if name, _ := m["name"].(string); !toolAvailable(name) {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}
