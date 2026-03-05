package monitor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

type Monitor struct {
	tmux *tmux.Client
}

func New(tmuxClient *tmux.Client) *Monitor {
	return &Monitor{tmux: tmuxClient}
}

// CheckStatus determines the session status based on the output mode.
func (m *Monitor) CheckStatus(s *session.Session) session.Status {
	switch s.OutputMode {
	case session.OutputModeStream:
		return m.checkStatusFromStreamJSONL(s)
	default:
		return m.checkStatusFromTerminal(s)
	}
}

// checkStatusFromStreamJSONL reads the agmux-managed stream JSONL file.
// Lifecycle:
//
//	system/init → working
//	assistant (stop_reason=null, streaming) → working
//	user → working
//	control_request → question_waiting (permission prompt)
//	result → idle (waiting for user input)
func (m *Monitor) checkStatusFromStreamJSONL(s *session.Session) session.Status {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return session.StatusWorking
	}
	jsonlPath := filepath.Join(streamsDir, s.ID+".jsonl")
	lastEntries := readLastStreamEntries(jsonlPath, 20)
	return classifyFromStreamEntries(lastEntries)
}

// checkStatusFromTerminal estimates status from tmux capture-pane output.
func (m *Monitor) checkStatusFromTerminal(s *session.Session) session.Status {
	content, err := m.tmux.CapturePane(s.TmuxSession, 50)
	if err != nil {
		return session.StatusWorking
	}
	return classifyFromTerminalContent(content)
}

// classifyFromTerminalContent does rough status estimation from terminal output.
func classifyFromTerminalContent(content string) session.Status {
	lines := strings.Split(strings.TrimSpace(content), "\n")

	// Walk from the bottom to find the last non-empty line
	lastLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastLine = trimmed
			break
		}
	}

	if lastLine == "" {
		return session.StatusWorking
	}

	// Claude Code shows a prompt like ">" or "❯" when waiting for user input
	if strings.HasPrefix(lastLine, ">") || strings.HasPrefix(lastLine, "❯") {
		return session.StatusIdle
	}

	// Check for yes/no or permission prompts
	lowerLine := strings.ToLower(lastLine)
	if strings.Contains(lowerLine, "(y/n)") ||
		strings.Contains(lowerLine, "[y/n]") ||
		strings.Contains(lowerLine, "allow") ||
		strings.Contains(lowerLine, "deny") {
		return session.StatusQuestionWaiting
	}

	// Check nearby lines for question patterns
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		trimmed := strings.TrimSpace(lines[i])
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "do you want to") ||
			strings.Contains(lower, "would you like") ||
			strings.Contains(lower, "shall i") {
			return session.StatusQuestionWaiting
		}
	}

	return session.StatusWorking
}

// --- Stream JSONL parsing (for stream mode) ---

type streamEntry struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

func readLastStreamEntries(path string, n int) []streamEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []streamEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var entry streamEntry
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

func classifyFromStreamEntries(entries []streamEntry) session.Status {
	if len(entries) == 0 {
		return session.StatusWorking
	}

	// Find the last meaningful entry (skip rate_limit_event etc.)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		switch e.Type {
		case "result":
			return session.StatusIdle
		case "control_request":
			return session.StatusQuestionWaiting
		case "assistant", "user", "system":
			return session.StatusWorking
		}
		// skip unknown types like rate_limit_event, continue searching
	}

	return session.StatusWorking
}
