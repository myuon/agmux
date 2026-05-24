package session

import (
	"os/exec"
)

// ProviderName identifies which CLI provider to use.
type ProviderName string

const (
	ProviderClaude ProviderName = "claude"
	ProviderCodex  ProviderName = "codex"
	ProviderCursor ProviderName = "cursor"
)

// StreamOpts contains parameters for building a stream-mode command.
type StreamOpts struct {
	SessionID      string
	ProjectPath    string
	MCPConfigPath  string
	SystemPrompt   string
	Resume         bool
	Worktree       bool
	ForkSession    bool   // fork from the resumed session (used with Resume + CLISessionID)
	CLISessionID   string // provider-specific session ID (for resume)
	InitialPrompt  string // initial user prompt (used by Codex as positional arg)
	Model          string // model to use (e.g. "claude-sonnet-4-5", "o4-mini")
	FullAuto       bool   // enable full-auto mode (Codex: --full-auto instead of --sandbox)
	APIPort        int    // API server port (used by Codex to set env vars for MCP)
	ClearOffset    int64  // byte offset in JSONL file; lines before this offset are hidden after Clear
}

// Provider abstracts CLI-specific behavior so agmux can support multiple AI CLIs.
type Provider interface {
	// BuildStreamCommand constructs the exec.Cmd for stream-json mode.
	BuildStreamCommand(opts StreamOpts) *exec.Cmd
	// ParseSessionID extracts a CLI session ID from a JSONL line.
	ParseSessionID(jsonlLine []byte) (string, bool)
	// ParseModel extracts the model name from a JSONL line (e.g. from system init or assistant events).
	ParseModel(jsonlLine []byte) (string, bool)
	// SetupMCP writes MCP config for this provider. Returns config file path.
	SetupMCP(sessionID string, port int) (string, error)
	// CleanupMCP removes MCP config for this provider.
	CleanupMCP(sessionID string) error
	// AppendOTelEnv appends OTel environment variables to the given env slice.
	AppendOTelEnv(env []string, port int) []string
	// Name returns the provider name.
	Name() ProviderName
	// IsOneShot returns true if the underlying CLI exits after each prompt
	// and must be re-spawned with --resume to continue the conversation.
	IsOneShot() bool
	// NormalizeStreamLine converts a provider-specific JSONL line into
	// zero or more Claude-compatible stream-json lines.
	//
	// Most providers return a single-element slice (line kept as-is or
	// converted in place). Providers that buffer multiple input events into
	// one logical output (e.g. cursor coalescing thinking/delta) may return:
	//   - nil or empty slice: drop this input line
	//   - one entry: a single output line
	//   - multiple entries: e.g. flush a buffered thinking message AND emit
	//     the current event (preserving original ordering).
	//
	// Callers must iterate the returned slice in order.
	NormalizeStreamLine(line []byte) [][]byte
	// ResetBuffers drops any per-session normalization state (e.g. partially
	// accumulated thinking text) for the given session ID without emitting
	// the buffered content. This is intended for use after replaying past
	// JSONL through NormalizeStreamLine (e.g. loadExistingLines), so that
	// state left over from a turn that never reached `completed` is not
	// carried into the live stream.
	//
	// Providers without buffered state should implement this as a no-op.
	ResetBuffers(sessionID string)
}

// GetProvider returns a Provider instance for the given name.
// Defaults to ClaudeProvider if name is empty or unrecognized.
// permissionMode is only used for ClaudeProvider.
func GetProvider(name ProviderName, command string, permissionMode string) Provider {
	switch name {
	case ProviderCodex:
		return NewCodexProvider(command)
	case ProviderCursor:
		return NewCursorProvider(command)
	default:
		return NewClaudeProvider(command, permissionMode)
	}
}
