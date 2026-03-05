package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

type Monitor struct {
	tmux          *tmux.Client
	lastFileSize  map[string]int64
	lastFileSizeMu sync.Mutex
}

func New(tmuxClient *tmux.Client) *Monitor {
	return &Monitor{
		tmux:         tmuxClient,
		lastFileSize: make(map[string]int64),
	}
}

// CheckStatusResult holds the detected status and the reason for the detection.
type CheckStatusResult struct {
	Status session.Status
	Reason string
}

// CheckStatus determines the session status based on the output mode.
func (m *Monitor) CheckStatus(s *session.Session) CheckStatusResult {
	switch s.OutputMode {
	case session.OutputModeStream:
		return m.checkStatusFromStreamJSONL(s)
	default:
		return m.checkStatusFromTerminal(s)
	}
}

// checkStatusFromStreamJSONL reads the agmux-managed stream JSONL file
// and uses LLM to classify the session status.
func (m *Monitor) checkStatusFromStreamJSONL(s *session.Session) CheckStatusResult {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return CheckStatusResult{session.StatusWorking, "streams dir error"}
	}
	jsonlPath := filepath.Join(streamsDir, s.ID+".jsonl")

	// Check if file has changed since last check
	info, err := os.Stat(jsonlPath)
	if err != nil {
		return CheckStatusResult{session.StatusWorking, "stat error"}
	}

	m.lastFileSizeMu.Lock()
	lastSize := m.lastFileSize[s.ID]
	currentSize := info.Size()
	m.lastFileSize[s.ID] = currentSize
	m.lastFileSizeMu.Unlock()

	if currentSize == lastSize {
		// No change, keep current status
		return CheckStatusResult{s.Status, "no change"}
	}

	// Get the last line of the file
	lastLine := readLastLine(jsonlPath)
	if lastLine == "" {
		return CheckStatusResult{session.StatusWorking, "empty file"}
	}

	// Use LLM to classify
	result := classifyWithLLM(lastLine)
	if result.Status != "" {
		return result
	}

	// Fallback to type-based classification
	lastEntries := readLastStreamEntries(jsonlPath, 20)
	return classifyFromStreamEntries(lastEntries)
}

const statusPrompt = `以下はClaude Codeセッションのstream-json出力の最後のエントリです。このセッションの現在の状態を判定してください。

以下のいずれか1つだけを出力してください（他の文字は一切不要）:
- working: AIが作業中（ツール実行中、コード生成中、思考中など）
- idle: 作業が完了してユーザーの次の指示を待っている状態
- question_waiting: ユーザーに質問や確認をしている状態

エントリ:
`

func classifyWithLLM(lastLine string) CheckStatusResult {
	// Truncate to avoid excessive token usage
	input := lastLine
	if len(input) > 2000 {
		input = input[len(input)-2000:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "text", statusPrompt+input)
	out, err := cmd.Output()
	if err != nil {
		return CheckStatusResult{"", fmt.Sprintf("llm error: %v", err)}
	}

	response := strings.TrimSpace(string(out))

	switch {
	case strings.Contains(response, "idle"):
		return CheckStatusResult{session.StatusIdle, fmt.Sprintf("llm: %s", truncate(response, 60))}
	case strings.Contains(response, "question_waiting"):
		return CheckStatusResult{session.StatusQuestionWaiting, fmt.Sprintf("llm: %s", truncate(response, 60))}
	case strings.Contains(response, "working"):
		return CheckStatusResult{session.StatusWorking, fmt.Sprintf("llm: %s", truncate(response, 60))}
	}

	return CheckStatusResult{"", fmt.Sprintf("llm unrecognized: %s", truncate(response, 60))}
}

func readLastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		lastLine = scanner.Text()
	}
	return lastLine
}

// checkStatusFromTerminal estimates status from tmux capture-pane output.
func (m *Monitor) checkStatusFromTerminal(s *session.Session) CheckStatusResult {
	content, err := m.tmux.CapturePane(s.TmuxSession, 50)
	if err != nil {
		return CheckStatusResult{session.StatusWorking, "capture-pane error"}
	}
	return classifyFromTerminalContent(content)
}

// classifyFromTerminalContent does rough status estimation from terminal output.
func classifyFromTerminalContent(content string) CheckStatusResult {
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
		return CheckStatusResult{session.StatusWorking, "empty output"}
	}

	// Claude Code shows a prompt like ">" or "❯" when waiting for user input
	if strings.HasPrefix(lastLine, ">") || strings.HasPrefix(lastLine, "❯") {
		return CheckStatusResult{session.StatusIdle, fmt.Sprintf("prompt detected: %q", truncate(lastLine, 60))}
	}

	// Check for yes/no or permission prompts
	lowerLine := strings.ToLower(lastLine)
	if strings.Contains(lowerLine, "(y/n)") ||
		strings.Contains(lowerLine, "[y/n]") ||
		strings.Contains(lowerLine, "allow") ||
		strings.Contains(lowerLine, "deny") {
		return CheckStatusResult{session.StatusQuestionWaiting, fmt.Sprintf("permission prompt: %q", truncate(lastLine, 60))}
	}

	// Check nearby lines for question patterns
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		trimmed := strings.TrimSpace(lines[i])
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "do you want to") ||
			strings.Contains(lower, "would you like") ||
			strings.Contains(lower, "shall i") {
			return CheckStatusResult{session.StatusQuestionWaiting, fmt.Sprintf("question: %q", truncate(trimmed, 60))}
		}
	}

	return CheckStatusResult{session.StatusWorking, fmt.Sprintf("last_line: %q", truncate(lastLine, 60))}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Stream JSONL parsing (fallback for stream mode) ---

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

func classifyFromStreamEntries(entries []streamEntry) CheckStatusResult {
	if len(entries) == 0 {
		return CheckStatusResult{session.StatusWorking, "no stream entries"}
	}

	// Find the last meaningful entry (skip rate_limit_event etc.)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		switch e.Type {
		case "result":
			return CheckStatusResult{session.StatusIdle, fmt.Sprintf("last_entry: %s", e.Type)}
		case "control_request":
			return CheckStatusResult{session.StatusQuestionWaiting, fmt.Sprintf("last_entry: %s", e.Type)}
		case "assistant", "user", "system":
			return CheckStatusResult{session.StatusWorking, fmt.Sprintf("last_entry: %s", e.Type)}
		}
	}

	return CheckStatusResult{session.StatusWorking, "no meaningful entries"}
}
