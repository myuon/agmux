package monitor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/myuon/agmux/internal/session"
)

type Monitor struct{}

func New() *Monitor {
	return &Monitor{}
}

// CheckStatus reads the session's JSONL log file and determines status.
func (m *Monitor) CheckStatus(s *session.Session) session.Status {
	jsonlPath := BuildJSONLPath(s.ProjectPath, s.ID)

	lastEntries := readLastEntries(jsonlPath, 20)
	return classifyFromEntries(lastEntries)
}

// BuildJSONLPath constructs the path to a session's JSONL log file.
func BuildJSONLPath(projectPath, sessionID string) string {
	homeDir, _ := os.UserHomeDir()
	escapedPath := strings.ReplaceAll(projectPath, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	return filepath.Join(homeDir, ".claude", "projects", escapedPath, sessionID+".jsonl")
}

type jsonlEntry struct {
	Type    string `json:"type"`
	Message *struct {
		StopReason *string         `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

func readLastEntries(path string, n int) []jsonlEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []jsonlEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var entry jsonlEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries
}

func classifyFromEntries(entries []jsonlEntry) session.Status {
	if len(entries) == 0 {
		return session.StatusWorking
	}

	// Find the last user or assistant entry (skip system, progress, queue-operation, etc.)
	var last *jsonlEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "user" || entries[i].Type == "assistant" {
			last = &entries[i]
			break
		}
	}

	if last == nil {
		return session.StatusWorking
	}

	if last.Type == "user" {
		return session.StatusWorking
	}

	if last.Type == "assistant" && last.Message != nil {
		stopReason := ""
		if last.Message.StopReason != nil {
			stopReason = *last.Message.StopReason
		}

		if stopReason == "end_turn" {
			if hasAskUserQuestion(last.Message.Content) {
				return session.StatusQuestionWaiting
			}
			return session.StatusIdle
		}

		if stopReason == "tool_use" {
			return session.StatusWorking
		}

		// stop_reason is null or empty → streaming
		return session.StatusWorking
	}

	return session.StatusWorking
}

func hasAskUserQuestion(content json.RawMessage) bool {
	if len(content) == 0 {
		return false
	}

	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return false
	}

	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "AskUserQuestion" {
			return true
		}
	}
	return false
}
