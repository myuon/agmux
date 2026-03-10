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
		args = append(args, "exec", "resume", opts.CLISessionID,
			"--json",
			"--sandbox", "danger-full-access",
		)
	} else {
		// Start a new session
		args = append(args, "exec",
			"--json",
			"--sandbox", "danger-full-access",
		)
	}

	if opts.MCPConfigPath != "" {
		args = append(args, "--mcp-config", opts.MCPConfigPath)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--instructions", opts.SystemPrompt)
	}

	// For non-resume, the prompt is required as the last positional argument.
	// We pass an initial prompt via stdin, so use a placeholder here.
	if !(opts.Resume && opts.CLISessionID != "") {
		args = append(args, "Follow the instructions given via stdin")
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
	// Codex emits: {"type":"thread.started","thread":{"id":"thr_xxx"}}
	var msg struct {
		Type   string `json:"type"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(jsonlLine, &msg) == nil && msg.Type == "thread.started" && msg.Thread.ID != "" {
		return msg.Thread.ID, true
	}
	return "", false
}

func (p *CodexProvider) BuildTerminalCommand(opts TerminalOpts) string {
	cmd := p.command + " exec --json --sandbox danger-full-access"
	if opts.Resume {
		// TODO: terminal resume for Codex is not yet fully supported
		cmd += " resume " + opts.SessionID
	}
	if opts.MCPConfigPath != "" {
		cmd += " --mcp-config " + opts.MCPConfigPath
	}
	if opts.SystemPrompt != "" {
		cmd += " --instructions " + shellQuote(opts.SystemPrompt)
	}
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

// NewCodexThreadStartedJSON creates a JSONL line for a thread.started event (for testing).
func NewCodexThreadStartedJSON(threadID string) string {
	evt := struct {
		Type   string `json:"type"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}{Type: "thread.started"}
	evt.Thread.ID = threadID
	b, _ := json.Marshal(evt)
	return string(b)
}
