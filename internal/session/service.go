package session

// SessionService defines the interface for session management operations.
// Both the CLI and Server use this interface to interact with sessions,
// ensuring consistent behavior regardless of the access path.
type SessionService interface {
	// List returns all sessions.
	List() ([]Session, error)
	// Get returns a session by ID.
	Get(id string) (*Session, error)
	// Create creates a new session.
	Create(name, projectPath, prompt string, worktree bool, opts ...CreateOpts) (*Session, error)
	// Duplicate creates a copy of an existing session.
	Duplicate(id string) (*Session, error)
	// Fork creates a new session by forking an existing session's conversation history.
	Fork(id string) (*Session, error)
	// Stop stops a running session.
	Stop(id string) error
	// Delete deletes a session.
	Delete(id string) error

	// SendKeys sends text to a session.
	SendKeys(id string, text string) error
	// SendKeysWithImages sends text and images to a session.
	SendKeysWithImages(id string, text string, images []ImageData) error
	// SendBtw sends an off-topic (btw) message to a session.
	SendBtw(id string, text string) error

	// Reconnect restarts the CLI process for an existing session.
	Reconnect(id string) error
	// Clear resets the session context.
	Clear(id string) error

	// UpdateStatus updates the session status.
	UpdateStatus(id string, status Status) error
	// UpdateContext updates the session's current task and goal.
	UpdateContext(id string, currentTask, goal string) error

	// CreateGoal creates a new goal for a session.
	CreateGoal(id string, currentTask, goal string, subgoal bool) error
	// CompleteGoal completes the current goal and returns the result.
	CompleteGoal(id string) (*CompleteGoalResult, error)

	// GetStreamLines returns stream lines for a session.
	GetStreamLines(id string, limit int) ([]string, int, error)
	// GetStreamLinesAfter returns stream lines added after the given index.
	GetStreamLinesAfter(id string, after int) ([]string, int, error)

	// SystemPrompt returns the system prompt used for sessions.
	SystemPrompt() string

	// CreateController creates or returns the singleton controller session.
	CreateController(projectPath string) (*Session, error)

	// SetOnNewLines sets a callback for real-time stream updates.
	SetOnNewLines(fn func(sessionID string, newLines []string, total int))

	// SetOnStatusChange sets a callback for real-time session status changes.
	SetOnStatusChange(fn func(sessionID string, status Status, lastError string))

	// IsStreamProcessAlive returns true if a stream process exists and has not exited.
	IsStreamProcessAlive(id string) bool

	// RecoverStreamProcesses restarts stream processes for all working sessions.
	RecoverStreamProcesses()
	// StopAllStreamProcesses gracefully stops all running stream processes.
	StopAllStreamProcesses()
	// SetCodexCommand sets the codex command for the manager.
	SetCodexCommand(cmd string)

	// ListRecentProjects returns recently used project paths.
	ListRecentProjects(limit int) ([]RecentProject, error)

	// ManagedHolderPIDs returns the PIDs of all holder processes currently managed.
	ManagedHolderPIDs() []int
}

// Verify Manager implements SessionService at compile time.
var _ SessionService = (*Manager)(nil)
