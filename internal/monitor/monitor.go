package monitor

import (
	"bufio"
	"context"
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
	Status  session.Status
	Reason  string
	Summary string // LLMによる要約（何をしている/何を聞いているか）
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
		return CheckStatusResult{Status: session.StatusWorking, Reason: "streams dir error"}
	}
	jsonlPath := filepath.Join(streamsDir, s.ID+".jsonl")

	// Check if file has changed since last check
	info, err := os.Stat(jsonlPath)
	if err != nil {
		return CheckStatusResult{Status: session.StatusWorking, Reason: "stat error"}
	}

	m.lastFileSizeMu.Lock()
	lastSize := m.lastFileSize[s.ID]
	currentSize := info.Size()
	m.lastFileSize[s.ID] = currentSize
	m.lastFileSizeMu.Unlock()

	if currentSize == lastSize {
		// No change, keep current status
		return CheckStatusResult{Status: s.Status, Reason: "no change"}
	}

	// Get the last line of the file
	lastLine := readLastLine(jsonlPath)
	if lastLine == "" {
		return CheckStatusResult{Status: session.StatusWorking, Reason: "empty file"}
	}

	// Use LLM to classify
	result := classifyWithLLM(lastLine)
	if result.Status != "" {
		return result
	}

	// LLM failed — keep current status
	return CheckStatusResult{Status: s.Status, Reason: fmt.Sprintf("llm failed, keeping current: %s", result.Reason)}
}

const StatusPrompt = `以下はClaude Codeセッションのstream-json出力の最後のエントリです。このセッションの現在の状態を判定してください。

1行目にステータス、2行目に日本語で短い要約（何をしている/何を聞いている/何を待っているか）を出力してください。

ステータス（1行目に以下のいずれか1つだけ）:
- working: AIが作業中（ツール実行中、コード生成中、思考中など）
- question_waiting: ユーザーに質問や確認をしている状態
- alignment_needed: タスクの方針決定や仕様の確認など、ユーザーとのアラインメントが必要な状態
- paused: ゴール未達成だが作業が中断している状態（次にやることはわかっている）
- idle: ゴールを達成し、ユーザーの次の指示を待っている状態（明確にタスク完了した場合のみ）

例:
question_waiting
コミットしてプッシュするかどうかを確認しています

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

	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "text", StatusPrompt+input)
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	out, err := cmd.Output()
	if err != nil {
		return CheckStatusResult{Reason: fmt.Sprintf("llm error: %v", err)}
	}

	response := strings.TrimSpace(string(out))
	lines := strings.SplitN(response, "\n", 2)
	statusLine := strings.TrimSpace(lines[0])
	summary := ""
	if len(lines) > 1 {
		summary = strings.TrimSpace(lines[1])
	}

	reason := fmt.Sprintf("llm: %s", truncate(statusLine, 60))

	switch {
	case strings.Contains(statusLine, "alignment_needed"):
		return CheckStatusResult{session.StatusAlignmentNeeded, reason, summary}
	case strings.Contains(statusLine, "question_waiting"):
		return CheckStatusResult{session.StatusQuestionWaiting, reason, summary}
	case strings.Contains(statusLine, "paused"):
		return CheckStatusResult{session.StatusPaused, reason, summary}
	case strings.Contains(statusLine, "idle"):
		return CheckStatusResult{session.StatusIdle, reason, summary}
	case strings.Contains(statusLine, "working"):
		return CheckStatusResult{session.StatusWorking, reason, summary}
	}

	return CheckStatusResult{Reason: fmt.Sprintf("llm unrecognized: %s", truncate(response, 60))}
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
		return CheckStatusResult{Status: session.StatusWorking, Reason: "capture-pane error"}
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
		return CheckStatusResult{Status: session.StatusWorking, Reason: "empty output"}
	}

	// Claude Code shows a prompt like ">" or "❯" when waiting for user input
	if strings.HasPrefix(lastLine, ">") || strings.HasPrefix(lastLine, "❯") {
		return CheckStatusResult{Status: session.StatusIdle, Reason: fmt.Sprintf("prompt detected: %q", truncate(lastLine, 60))}
	}

	// Check for yes/no or permission prompts
	lowerLine := strings.ToLower(lastLine)
	if strings.Contains(lowerLine, "(y/n)") ||
		strings.Contains(lowerLine, "[y/n]") ||
		strings.Contains(lowerLine, "allow") ||
		strings.Contains(lowerLine, "deny") {
		return CheckStatusResult{Status: session.StatusQuestionWaiting, Reason: fmt.Sprintf("permission prompt: %q", truncate(lastLine, 60))}
	}

	// Check nearby lines for question patterns
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		trimmed := strings.TrimSpace(lines[i])
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "do you want to") ||
			strings.Contains(lower, "would you like") ||
			strings.Contains(lower, "shall i") {
			return CheckStatusResult{Status: session.StatusQuestionWaiting, Reason: fmt.Sprintf("question: %q", truncate(trimmed, 60))}
		}
	}

	return CheckStatusResult{Status: session.StatusWorking, Reason: fmt.Sprintf("last_line: %q", truncate(lastLine, 60))}
}

func filterEnv(env []string, exclude string) []string {
	var filtered []string
	prefix := exclude + "="
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

