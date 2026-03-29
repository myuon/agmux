package session

// StreamProcessInterface abstracts the common interface between
// StreamProcess (direct pipe) and HolderStreamProcess (Unix socket).
type StreamProcessInterface interface {
	// SessionID returns the CLI session ID.
	SessionID() string

	// Send writes a user message.
	Send(message string) error
	// SendWithImages writes a user message with optional images.
	SendWithImages(message string, images []ImageData) error

	// SetOnSessionID sets a callback for CLI session ID capture.
	SetOnSessionID(fn func(cliSessionID string))
	// SetOnModel sets a callback for model name capture.
	SetOnModel(fn func(model string))
	// SetOnNewLines sets a callback for new stream lines.
	SetOnNewLines(fn func(sessionID string, newLines []string, total int))
	// SetOnProcessExit sets a callback for process exit.
	SetOnProcessExit(fn func(sessionID string, exitErr error))
	// SetOnTurnComplete sets a callback for when the CLI completes a turn
	// (detected via result event with subtype "success").
	SetOnTurnComplete(fn func(sessionID string))

	// GetLines returns the last N lines.
	GetLines(limit int) []string
	// GetLinesAfter returns lines after the given index.
	GetLinesAfter(after int) ([]string, int)
	// TotalLines returns the total line count.
	TotalLines() int
	// ClearLines clears the in-memory lines buffer (used on Clear).
	ClearLines()

	// Stop stops the process.
	Stop()
	// Done returns a channel closed when the process exits.
	Done() <-chan struct{}
	// IsExited returns true if the process has exited.
	IsExited() bool

	// recordUserMessage records a user message without sending it to stdin.
	recordUserMessage(message string)

	// ProviderName returns the name of the provider used by this process.
	ProviderName() ProviderName
}

// Verify both implementations satisfy the interface at compile time.
var _ StreamProcessInterface = (*StreamProcess)(nil)
var _ StreamProcessInterface = (*HolderStreamProcess)(nil)
