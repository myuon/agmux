package session

import "encoding/json"

// subagentLine is the minimal shape needed to classify a stream line into a
// subagent thread.
type subagentLine struct {
	Type            string  `json:"type"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// isAgentToolName reports whether the tool name spawns a subagent.
func isAgentToolName(name string) bool {
	return name == "Agent" || name == "Task"
}

// FilterSubagentLines returns the lines that belong to the subagent thread
// spawned by the Agent/Task tool_use with the given id: lines whose
// parent_tool_use_id is that id, plus lines of nested subagents (descendants).
// When a line in the set contains an assistant tool_use block named
// Agent/Task, that tool_use id is added to the set. Lines are scanned in
// order, so nested children (which always follow their parent tool_use) are
// picked up in a single pass.
func FilterSubagentLines(lines []string, parentToolUseID string) []string {
	result := []string{}
	if parentToolUseID == "" {
		return result
	}
	ids := map[string]bool{parentToolUseID: true}
	for _, line := range lines {
		var l subagentLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue
		}
		if l.ParentToolUseID == nil || !ids[*l.ParentToolUseID] {
			continue
		}
		result = append(result, line)
		if l.Type != "assistant" || l.Message == nil {
			continue
		}
		var blocks []assistantBlock
		if err := json.Unmarshal(l.Message.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" && isAgentToolName(b.Name) {
				ids[b.ID] = true
			}
		}
	}
	return result
}
