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
)

type Monitor struct {
	lastFileSize   map[string]int64
	lastFileSizeMu sync.Mutex
}

func New() *Monitor {
	return &Monitor{
		lastFileSize: make(map[string]int64),
	}
}

// CheckStatusResult holds the detected status and the reason for the detection.
type CheckStatusResult struct {
	Status  session.Status
	Reason  string
	Summary string // LLMによる要約（何をしている/何を聞いているか）
}

// CheckStatus determines the session status from the stream JSONL file.
func (m *Monitor) CheckStatus(s *session.Session) CheckStatusResult {
	return m.checkStatusFromStreamJSONL(s)
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
- question_waiting: ユーザーに質問や確認をしている状態
- alignment_needed: タスクの方針決定や仕様の確認など、ユーザーとのアラインメントが必要な状態
- paused: ゴール未達成だが作業が中断している状態（次にやることはわかっている）
- none: 上記に該当しない（判定不要）

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
	case strings.Contains(statusLine, "none"):
		// LLM says none of the special statuses apply; keep current status
		return CheckStatusResult{Status: "", Reason: "llm: none"}
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

// ShouldNudge checks if a session should receive a nudge message.
// Returns true when both conditions are met:
// 1. The last JSONL event contains a tool_use content block
// 2. The JSONL file was last modified 30+ minutes ago
func (m *Monitor) ShouldNudge(s *session.Session) bool {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return false
	}
	jsonlPath := filepath.Join(streamsDir, s.ID+".jsonl")

	info, err := os.Stat(jsonlPath)
	if err != nil {
		return false
	}

	// Condition 2: last modified 30+ minutes ago
	if time.Since(info.ModTime()) < 30*time.Minute {
		return false
	}

	// Condition 1: last line contains tool_use
	lastLine := readLastLine(jsonlPath)
	if lastLine == "" {
		return false
	}

	return lastLineHasToolUse(lastLine)
}

// lastLineHasToolUse checks if a JSONL line contains a tool_use content block.
func lastLineHasToolUse(line string) bool {
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return false
	}
	if event.Type != "assistant" {
		return false
	}

	var contentBlocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(event.Message.Content, &contentBlocks); err != nil {
		return false
	}

	for _, block := range contentBlocks {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
