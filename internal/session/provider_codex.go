package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/myuon/agmux/internal/db"
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
		// Note: --sandbox is not supported for exec resume
		args = append(args, "exec", "resume",
			"--json",
			opts.CLISessionID,
		)
	} else {
		// Start a new session
		args = append(args, "exec",
			"--json",
			"--sandbox", "danger-full-access",
		)
	}

	// Codex CLI does not support --mcp-config or --instructions flags.

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

func (p *CodexProvider) BuildTerminalCommand(opts TerminalOpts) string {
	otelPrefix := p.OTelEnvPrefix(opts.APIPort)

	var cmd string
	if opts.Resume {
		cmd = otelPrefix + p.command + " resume " + opts.SessionID + " --sandbox danger-full-access"
	} else {
		cmd = otelPrefix + p.command + " --sandbox danger-full-access"
	}

	// Codex CLI does not support --mcp-config or --instructions flags.

	return cmd
}

func (p *CodexProvider) SetupMCP(sessionID string, port int) (string, error) {
	return writeMCPConfig(sessionID, port)
}

func (p *CodexProvider) CleanupMCP(sessionID string) error {
	dir, err := db.AgmuxDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "mcp-configs", sessionID+".json")
	return os.Remove(path)
}

func (p *CodexProvider) AppendOTelEnv(env []string, port int) []string {
	// Codex CLI does not support OTel yet
	return env
}

func (p *CodexProvider) OTelEnvPrefix(port int) string {
	return ""
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
