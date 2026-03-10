package session

import (
	"os/exec"
)

// CodexProvider is a stub implementation of Provider for OpenAI Codex CLI.
// Full implementation will be added in a future PR.
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
	// Stub: will be implemented when Codex CLI support is added
	cmd := exec.Command(p.command)
	cmd.Dir = opts.ProjectPath
	return cmd
}

func (p *CodexProvider) ParseSessionID(jsonlLine []byte) (string, bool) {
	// Stub
	return "", false
}

func (p *CodexProvider) BuildTerminalCommand(opts TerminalOpts) string {
	// Stub
	return p.command
}

func (p *CodexProvider) SetupMCP(sessionID string, port int) (string, error) {
	// Codex doesn't support MCP yet; reuse the same config format for now
	return writeMCPConfig(sessionID, port)
}

func (p *CodexProvider) CleanupMCP(sessionID string) error {
	return nil
}

func (p *CodexProvider) AppendOTelEnv(env []string, port int) []string {
	// Stub: no OTel for Codex yet
	return env
}

func (p *CodexProvider) OTelEnvPrefix(port int) string {
	return ""
}
