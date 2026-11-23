package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var claudeProjectSlugRe = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeSubagentsDir returns the directory where the Claude CLI writes the
// per-subagent transcripts of a CLI session:
// <homeDir>/.claude/projects/<projectPath slug>/<cliSessionID>/subagents
func ClaudeSubagentsDir(homeDir, projectPath, cliSessionID string) string {
	slug := claudeProjectSlugRe.ReplaceAllString(projectPath, "-")
	return filepath.Join(homeDir, ".claude", "projects", slug, cliSessionID, "subagents")
}

type subagentMeta struct {
	ToolUseID string `json:"toolUseId"`
}

// transcriptLine is the subset of a subagent transcript line we convert.
type transcriptLine struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
}

type transcriptStreamLine struct {
	Type            string          `json:"type"`
	Message         json.RawMessage `json:"message"`
	ParentToolUseID string          `json:"parent_tool_use_id"`
	SessionID       string          `json:"session_id,omitempty"`
	UUID            string          `json:"uuid,omitempty"`
	Timestamp       string          `json:"timestamp,omitempty"`
}

// ReadSubagentTranscriptLines reads the transcript that the Claude CLI wrote
// for the subagent spawned by the Agent/Task tool_use parentToolUseID, from
// subagentsDir (see ClaudeSubagentsDir). This is how background subagents,
// whose messages are not emitted into the stream-json, are shown.
//
// Each user/assistant transcript line is converted into the stream-line shape
// (with parent_tool_use_id set to the spawning tool_use id). Transcripts of
// nested subagents (spawned by an Agent/Task tool_use inside a transcript) are
// included with their own parent_tool_use_id. Lines are ordered by timestamp.
// Missing or unreadable files yield an empty result.
func ReadSubagentTranscriptLines(subagentsDir, parentToolUseID string) []string {
	return ReadSubagentTranscriptLinesByAgent(subagentsDir, parentToolUseID, "")
}

var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ReadSubagentTranscriptLinesByAgent is ReadSubagentTranscriptLines, but when
// agentID (the task id) is given, the transcript agent-<agentID>.jsonl is used
// for parentToolUseID even if no meta.json records that tool_use id. This
// covers background subagents resumed by SendMessage: their task events carry
// the SendMessage tool_use id while the meta keeps the original Agent one.
func ReadSubagentTranscriptLinesByAgent(subagentsDir, parentToolUseID, agentID string) []string {
	result := []string{}
	if parentToolUseID == "" || subagentsDir == "" {
		return result
	}
	metas, err := filepath.Glob(filepath.Join(subagentsDir, "agent-*.meta.json"))
	if err != nil {
		return result
	}
	// toolUseId -> transcript path
	transcripts := map[string]string{}
	for _, metaPath := range metas {
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var m subagentMeta
		if err := json.Unmarshal(data, &m); err != nil || m.ToolUseID == "" {
			continue
		}
		transcripts[m.ToolUseID] = strings.TrimSuffix(metaPath, ".meta.json") + ".jsonl"
	}
	if agentID != "" && agentIDRe.MatchString(agentID) {
		path := filepath.Join(subagentsDir, "agent-"+agentID+".jsonl")
		if _, err := os.Stat(path); err == nil {
			transcripts[parentToolUseID] = path
		}
	}

	type entry struct {
		ts   string
		line string
	}
	var entries []entry
	visited := map[string]bool{}
	queue := []string{parentToolUseID}
	for len(queue) > 0 {
		toolUseID := queue[0]
		queue = queue[1:]
		if visited[toolUseID] {
			continue
		}
		visited[toolUseID] = true
		path, ok := transcripts[toolUseID]
		if !ok {
			continue
		}
		lines, nested := readTranscriptFile(path, toolUseID)
		for _, l := range lines {
			entries = append(entries, entry{ts: l.Timestamp, line: l.encoded})
		}
		queue = append(queue, nested...)
	}

	// Stable sort keeps file order for equal timestamps, and
	// places nested transcripts after the tool_use that spawned them.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ts < entries[j].ts
	})
	for _, e := range entries {
		result = append(result, e.line)
	}
	return result
}

type convertedLine struct {
	Timestamp string
	encoded   string
}

// readTranscriptFile converts the user/assistant lines of one transcript and
// returns the ids of Agent/Task tool_use blocks found in assistant lines.
func readTranscriptFile(path, parentToolUseID string) ([]convertedLine, []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var out []convertedLine
	var nested []string
	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 {
			var tl transcriptLine
			if json.Unmarshal(raw, &tl) == nil && (tl.Type == "user" || tl.Type == "assistant") && len(tl.Message) > 0 {
				b, mErr := json.Marshal(transcriptStreamLine{
					Type:            tl.Type,
					Message:         tl.Message,
					ParentToolUseID: parentToolUseID,
					SessionID:       tl.SessionID,
					UUID:            tl.UUID,
					Timestamp:       tl.Timestamp,
				})
				if mErr == nil {
					out = append(out, convertedLine{Timestamp: tl.Timestamp, encoded: string(b)})
				}
				if tl.Type == "assistant" {
					nested = append(nested, agentToolUseIDs(tl.Message)...)
				}
			}
		}
		if err != nil {
			break
		}
	}
	return out, nested
}

func agentToolUseIDs(message json.RawMessage) []string {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(message, &m); err != nil {
		return nil
	}
	var blocks []assistantBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	var ids []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID != "" && isAgentToolName(b.Name) {
			ids = append(ids, b.ID)
		}
	}
	return ids
}
