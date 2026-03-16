package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// CodexProvider implements Provider for OpenAI Codex CLI.
type CodexProvider struct {
	command string
}

// NewCodexProvider creates a CodexProvider with the given command.
func NewCodexProvider(command string) *CodexProvider {
	if command == "" {
		command = "codex"
	}
	return &CodexProvider{command: command}
}

func (p *CodexProvider) Name() ProviderName {
	return ProviderCodex
}

func (p *CodexProvider) BuildStreamCommand(opts StreamOpts) *exec.Cmd {
	var args []string

	if opts.Resume && opts.CLISessionID != "" {
		// Resume an existing session
		// Note: --sandbox flag is not supported for exec resume, so we use
		// --dangerously-bypass-approvals-and-sandbox to retain full write access.
		args = append(args, "exec", "resume",
			"--json",
			"--dangerously-bypass-approvals-and-sandbox",
		)
		args = append(args, opts.CLISessionID)
	} else {
		// Start a new session
		// Use --dangerously-bypass-approvals-and-sandbox for consistency with resume mode.
		// Do NOT use --full-auto as it forces --sandbox workspace-write.
		args = append(args, "exec",
			"--json",
			"--dangerously-bypass-approvals-and-sandbox",
		)
	}

	// Codex CLI does not support --mcp-config or --instructions flags.

	// Add --model flag if specified
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}

	// For non-resume, the prompt is required as the last positional argument.
	// If SystemPrompt is set, prepend it to the prompt.
	if !(opts.Resume && opts.CLISessionID != "") {
		prompt := opts.InitialPrompt
		if prompt == "" {
			prompt = "Follow the instructions given via stdin"
		}
		if opts.SystemPrompt != "" {
			prompt = opts.SystemPrompt + "\n\n" + prompt
		}
		args = append(args, prompt)
	}

	// For resume, a follow-up message can be passed as the last positional arg.
	if opts.Resume && opts.CLISessionID != "" && opts.InitialPrompt != "" {
		args = append(args, opts.InitialPrompt)
	}

	cmd := exec.Command(p.command, args...)
	cmd.Dir = opts.ProjectPath
	// Filter out CLAUDECODE env var to avoid nested session detection
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	// Set session-specific env vars for global MCP server
	if opts.SessionID != "" {
		cmd.Env = append(cmd.Env, "AGMUX_SESSION_ID="+opts.SessionID)
	}
	if opts.APIPort > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AGMUX_API_URL=http://localhost:%d", opts.APIPort))
	}
	return cmd
}

func (p *CodexProvider) ParseSessionID(jsonlLine []byte) (string, bool) {
	// Codex emits: {"type":"thread.started","thread_id":"019cdd4c-..."}
	var msg struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}
	if json.Unmarshal(jsonlLine, &msg) == nil && msg.Type == "thread.started" && msg.ThreadID != "" {
		return msg.ThreadID, true
	}
	return "", false
}

func (p *CodexProvider) ParseModel(jsonlLine []byte) (string, bool) {
	// Codex events may contain a model field in various event types (e.g. turn.completed).
	// We accept any event that has a non-empty "model" field.
	var msg struct {
		Type  string `json:"type"`
		Model string `json:"model"`
	}
	if json.Unmarshal(jsonlLine, &msg) == nil && msg.Model != "" {
		return msg.Model, true
	}
	return "", false
}

func (p *CodexProvider) SetupMCP(sessionID string, port int) (string, error) {
	// Codex uses global MCP registration (codex mcp add agmux -- agmux mcp).
	// Session-specific env vars (AGMUX_SESSION_ID, AGMUX_API_URL) are passed
	// via cmd.Env in BuildStreamCommand, so no per-session config file is needed.
	return "", nil
}

func (p *CodexProvider) CleanupMCP(sessionID string) error {
	// No per-session MCP config file to clean up for Codex.
	return nil
}

func (p *CodexProvider) AppendOTelEnv(env []string, port int) []string {
	// Codex CLI does not support OTel yet
	return env
}

// NormalizeStreamLine converts a Codex JSONL event into Claude-compatible
// stream-json format so the frontend can render it uniformly.
func (p *CodexProvider) NormalizeStreamLine(line []byte) []byte {
	var envelope struct {
		Type string          `json:"type"`
		Item json.RawMessage `json:"item"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return line
	}

	switch envelope.Type {
	case "item.completed":
		return p.normalizeItemCompleted(envelope.Item, line)
	case "item.started":
		// Skip in-progress events; they have no useful content yet.
		return nil
	default:
		// thread.started, turn.started, turn.completed, etc. – keep as-is.
		return line
	}
}

func (p *CodexProvider) normalizeItemCompleted(raw json.RawMessage, original []byte) []byte {
	var item struct {
		Type             string  `json:"type"`
		Text             string  `json:"text"`
		Command          string  `json:"command"`
		AggregatedOutput string  `json:"aggregated_output"`
		ExitCode         *int    `json:"exit_code"`
		Status           string  `json:"status"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return original
	}

	switch item.Type {
	case "agent_message":
		return p.buildAssistantText(item.Text)
	case "command_execution":
		return p.buildAssistantToolUse(item.Command, item.AggregatedOutput)
	default:
		return original
	}
}

func (p *CodexProvider) buildAssistantText(text string) []byte {
	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}{Type: "assistant"}
	msg.Message.Role = "assistant"
	msg.Message.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}
	b, _ := json.Marshal(msg)
	return b
}

func (p *CodexProvider) buildAssistantToolUse(command, output string) []byte {
	type toolUseBlock struct {
		Type  string            `json:"type"`
		Name  string            `json:"name,omitempty"`
		Input map[string]string `json:"input,omitempty"`
	}
	type toolResultBlock struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	// We need a heterogeneous content array, so use json.RawMessage.
	tu := toolUseBlock{Type: "tool_use", Name: "Bash", Input: map[string]string{"command": command}}
	tr := toolResultBlock{Type: "tool_result", Content: output}

	tuJSON, _ := json.Marshal(tu)
	trJSON, _ := json.Marshal(tr)

	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}{Type: "assistant"}
	msg.Message.Role = "assistant"
	msg.Message.Content = []json.RawMessage{tuJSON, trJSON}
	b, _ := json.Marshal(msg)
	return b
}

// ReadCodexDefaultModel reads the default model from ~/.codex/config.toml.
// Returns empty string if the file doesn't exist or has no model field.
func ReadCodexDefaultModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	configPath := filepath.Join(home, ".codex", "config.toml")

	var cfg struct {
		Model string `toml:"model"`
	}
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return ""
	}
	return cfg.Model
}

// NewCodexThreadStartedJSON creates a JSONL line for a thread.started event (for testing).
// Matches real Codex CLI output format: {"type":"thread.started","thread_id":"..."}
func NewCodexThreadStartedJSON(threadID string) string {
	evt := struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}{
		Type:     "thread.started",
		ThreadID: threadID,
	}
	b, _ := json.Marshal(evt)
	return string(b)
}
