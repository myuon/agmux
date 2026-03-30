package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/db"
)


// ClaudeProvider implements Provider for Claude Code CLI.
type ClaudeProvider struct {
	command        string // e.g. "claude"
	permissionMode string // e.g. "bypassPermissions"
}

// NewClaudeProvider creates a ClaudeProvider with the given command and permission mode.
// If command is empty, defaults to "claude".
// If permissionMode is empty or invalid, defaults to config.DefaultPermissionMode.
func NewClaudeProvider(command string, permissionMode string) *ClaudeProvider {
	if command == "" {
		command = "claude"
	}
	if permissionMode == "" || !config.IsValidPermissionMode(permissionMode) {
		permissionMode = config.DefaultPermissionMode
	}
	return &ClaudeProvider{command: command, permissionMode: permissionMode}
}

func (p *ClaudeProvider) Name() ProviderName {
	return ProviderClaude
}

func (p *ClaudeProvider) BuildStreamCommand(opts StreamOpts) *exec.Cmd {
	sessionFlag := "--session-id"
	resumeID := opts.SessionID
	if opts.Resume {
		if opts.CLISessionID != "" {
			sessionFlag = "--resume"
			resumeID = opts.CLISessionID
		}
	} else if opts.CLISessionID != "" {
		resumeID = opts.CLISessionID
	}

	// Claude CLI requires a valid UUID for --session-id.
	// When not resuming and no CLI session ID is set, generate a UUID
	// instead of using the agmux nanoid.
	if sessionFlag == "--session-id" {
		if _, err := uuid.Parse(resumeID); err != nil {
			resumeID = uuid.New().String()
		}
	}

	args := []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		sessionFlag, resumeID,
		"--permission-mode", p.permissionMode,
		"--include-partial-messages",
	}
	if opts.ForkSession {
		args = append(args, "--fork-session")
	}
	if opts.MCPConfigPath != "" {
		args = append(args, "--permission-prompt-tool", "mcp__agmux__permission_prompt")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.MCPConfigPath != "" {
		args = append(args, "--mcp-config", opts.MCPConfigPath)
	}
	args = append(args, "--append-system-prompt", opts.SystemPrompt)
	if opts.Worktree {
		args = append(args, "--worktree")
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = opts.ProjectPath
	// Filter out CLAUDECODE env var to avoid nested session detection
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	// Prevent git from hanging on interactive auth prompts (e.g. HTTPS push)
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func (p *ClaudeProvider) ParseSessionID(jsonlLine []byte) (string, bool) {
	var msg struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(jsonlLine, &msg) == nil && msg.Type == "system" && msg.SessionID != "" {
		return msg.SessionID, true
	}
	return "", false
}

func (p *ClaudeProvider) ParseModel(jsonlLine []byte) (string, bool) {
	// Try system init event: {"type":"system","subtype":"init","model":"claude-sonnet-4-5-20250514",...}
	var sysMsg struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
	}
	if json.Unmarshal(jsonlLine, &sysMsg) == nil && sysMsg.Type == "system" && sysMsg.Subtype == "init" && sysMsg.Model != "" {
		return sysMsg.Model, true
	}

	// Try assistant message: {"type":"assistant","message":{"model":"claude-sonnet-4-5-20250514",...}}
	var assistantMsg struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(jsonlLine, &assistantMsg) == nil && assistantMsg.Type == "assistant" && assistantMsg.Message.Model != "" {
		return assistantMsg.Message.Model, true
	}

	return "", false
}

func (p *ClaudeProvider) SetupMCP(sessionID string, port int) (string, error) {
	return writeMCPConfig(sessionID, port)
}

func (p *ClaudeProvider) CleanupMCP(sessionID string) error {
	dir, err := db.AgmuxDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "mcp-configs", sessionID+".json")
	return os.Remove(path)
}

func (p *ClaudeProvider) AppendOTelEnv(env []string, port int) []string {
	if port == 0 {
		cfg, err := config.Load()
		if err == nil {
			port = cfg.Server.Port
		} else {
			port = config.Default().Server.Port
		}
	}

	otelVars := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_LOGS_EXPORTER":          "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT": fmt.Sprintf("http://localhost:%d", port),
		"OTEL_METRIC_EXPORT_INTERVAL": "30000",
		"OTEL_LOGS_EXPORT_INTERVAL":   "5000",
	}

	existing := make(map[string]bool)
	for _, e := range env {
		key := strings.SplitN(e, "=", 2)[0]
		existing[key] = true
	}

	for k, v := range otelVars {
		if !existing[k] {
			env = append(env, k+"="+v)
		}
	}

	return env
}

func (p *ClaudeProvider) NormalizeStreamLine(line []byte) []byte {
	// Claude lines are already in the expected format; return as-is.
	return line
}

