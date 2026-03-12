package session

import (
	"os/exec"
)

// ProviderName identifies which CLI provider to use.
type ProviderName string

const (
	ProviderClaude ProviderName = "claude"
	ProviderCodex  ProviderName = "codex"
)

// StreamOpts contains parameters for building a stream-mode command.
type StreamOpts struct {
	SessionID      string
	ProjectPath    string
	MCPConfigPath  string
	SystemPrompt   string
	Resume         bool
	Worktree       bool
	CLISessionID   string // provider-specific session ID (for resume)
	InitialPrompt  string // initial user prompt (used by Codex as positional arg)
	Model          string // model name (used by Codex --model flag)
}

// TerminalOpts contains parameters for building a terminal-mode command.
type TerminalOpts struct {
	SessionID     string
	MCPConfigPath string
	SystemPrompt  string
	Resume        bool
	APIPort       int
}

// Provider abstracts CLI-specific behavior so agmux can support multiple AI CLIs.
type Provider interface {
	// BuildStreamCommand constructs the exec.Cmd for stream-json mode.
	BuildStreamCommand(opts StreamOpts) *exec.Cmd
	// ParseSessionID extracts a CLI session ID from a JSONL line.
	ParseSessionID(jsonlLine []byte) (string, bool)
	// BuildTerminalCommand returns the shell command string for terminal mode.
	BuildTerminalCommand(opts TerminalOpts) string
	// SetupMCP writes MCP config for this provider. Returns config file path.
	SetupMCP(sessionID string, port int) (string, error)
	// CleanupMCP removes MCP config for this provider.
	CleanupMCP(sessionID string) error
	// AppendOTelEnv appends OTel environment variables to the given env slice.
	AppendOTelEnv(env []string, port int) []string
	// OTelEnvPrefix returns a shell env prefix string for terminal mode.
	OTelEnvPrefix(port int) string
	// Name returns the provider name.
	Name() ProviderName
	// NormalizeStreamLine converts a provider-specific JSONL line into
	// Claude-compatible stream-json format. If the line should be kept as-is,
	// it returns the original bytes unchanged. If the line should be dropped,
	// it returns nil.
	NormalizeStreamLine(line []byte) []byte
}

// GetProvider returns a Provider instance for the given name.
// Defaults to ClaudeProvider if name is empty or unrecognized.
func GetProvider(name ProviderName, command string) Provider {
	switch name {
	case ProviderCodex:
		return NewCodexProvider(command)
	default:
		return NewClaudeProvider(command)
	}
}
