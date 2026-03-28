package monitor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
)

type Monitor struct{}

func New() *Monitor {
	return &Monitor{}
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

