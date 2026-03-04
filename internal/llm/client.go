package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Client struct {
	model   string
	timeout time.Duration
}

func New(model string) *Client {
	return &Client{
		model:   model,
		timeout: 60 * time.Second,
	}
}

type AnalysisResult struct {
	Status   string `json:"status"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	SendText string `json:"send_text"`
}

func (c *Client) Analyze(prompt string) (*AnalysisResult, error) {
	args := []string{"-p", prompt, "--output-format", "text"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	cmd := exec.Command("claude", args...)
	// Unset CLAUDECODE to avoid nested session detection
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("claude command: %w: %s", err, string(out))
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, fmt.Errorf("empty response from claude")
	}

	jsonStr := extractJSON(text)

	var result AnalysisResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("parse analysis result: %w (raw: %s)", err, text)
	}

	return &result, nil
}

func extractJSON(text string) string {
	start := -1
	end := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '{' && start == -1 {
			start = i
		}
		if text[i] == '}' {
			end = i + 1
		}
	}
	if start >= 0 && end > start {
		return text[start:end]
	}
	return text
}

// filterEnv returns env vars with the specified key removed.
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	var filtered []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
