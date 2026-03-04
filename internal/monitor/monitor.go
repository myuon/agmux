package monitor

import (
	"strings"

	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
)

type Monitor struct {
	tmux *tmux.Client
}

func New(tmuxClient *tmux.Client) *Monitor {
	return &Monitor{tmux: tmuxClient}
}

// CheckStatus inspects a session's tmux pane and classifies its status.
// Returns (status, capturedOutput, error).
func (m *Monitor) CheckStatus(s *session.Session) (session.Status, string, error) {
	if !m.tmux.HasSessionByFullName(s.TmuxSession) {
		return session.StatusStopped, "", nil
	}

	output, err := m.tmux.CapturePane(s.TmuxSession, 100)
	if err != nil {
		return session.StatusError, "", err
	}

	status := classifyOutput(output)
	return status, output, nil
}

func classifyOutput(output string) session.Status {
	// Look at the last ~20 lines for prompt detection
	lines := strings.Split(strings.TrimSpace(output), "\n")
	tail := output
	if len(lines) > 20 {
		tail = strings.Join(lines[len(lines)-20:], "\n")
	}

	lower := strings.ToLower(tail)

	// Waiting patterns (permission prompts, user input requests)
	waitingPatterns := []string{
		"do you want to proceed",
		"allow",
		"[y/n]",
		"(y/n)",
		"press enter",
		"yes/no",
		"approve",
		"permission",
		"would you like",
	}
	for _, p := range waitingPatterns {
		if strings.Contains(lower, p) {
			return session.StatusWaiting
		}
	}

	// Error patterns
	errorPatterns := []string{
		"error:",
		"fatal:",
		"panic:",
		"failed",
		"exception",
		"traceback",
	}
	for _, p := range errorPatterns {
		if strings.Contains(lower, p) {
			return session.StatusError
		}
	}

	// Done patterns (shell prompt visible at the end, meaning claude exited)
	lastLine := ""
	if len(lines) > 0 {
		lastLine = strings.TrimSpace(lines[len(lines)-1])
	}
	if lastLine == "$" || strings.HasSuffix(lastLine, "$ ") || strings.HasSuffix(lastLine, "% ") {
		return session.StatusDone
	}

	return session.StatusRunning
}
