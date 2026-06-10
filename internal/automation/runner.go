package automation

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
)

// SessionCreator is the subset of session.SessionService the runner needs.
// session.Manager satisfies it.
type SessionCreator interface {
	Create(name, projectPath, prompt string, worktree bool, opts ...session.CreateOpts) (*session.Session, error)
	Get(id string) (*session.Session, error)
	UpdateStatus(id string, status session.Status) error
}

// Runner executes a fired automation: it creates a new session and sends the
// automation prompt to it. When the session created by the previous run is
// still active, the firing is skipped and recorded as such.
type Runner struct {
	store    *Store
	sessions SessionCreator
	logger   *slog.Logger
	// controllerDir resolves the working directory for automations without a
	// project. Defaults to db.ControllerDir; injectable for tests.
	controllerDir func() (string, error)
}

// NewRunner creates a Runner backed by the given store and session service.
func NewRunner(store *Store, sessions SessionCreator, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		store:         store,
		sessions:      sessions,
		logger:        logger.With("component", "automation_runner"),
		controllerDir: db.ControllerDir,
	}
}

// Run executes one firing of the automation and records the outcome in the
// run history. It never returns an error: failures are recorded as error
// runs and logged.
func (r *Runner) Run(a Automation, firedAt time.Time) *Run {
	run := Run{AutomationID: a.ID, FiredAt: firedAt}

	// Multi-run guard: skip when the session created by the previous run is
	// still active.
	if sessID, active := r.previousRunActive(a.ID); active {
		run.Status = RunSkipped
		run.Message = fmt.Sprintf("previous run session %s is still active", sessID)
		r.logger.Info("automation skipped: previous run still active",
			"automationId", a.ID, "name", a.Name, "sessionId", sessID)
		return r.record(run)
	}

	projectPath := a.ProjectPath
	if projectPath == "" {
		dir, err := r.controllerDir()
		if err != nil {
			run.Status = RunError
			run.Message = fmt.Sprintf("resolve controller dir: %v", err)
			r.logger.Error("automation failed: controller dir", "automationId", a.ID, "error", err)
			return r.record(run)
		}
		projectPath = dir
	}

	name := fmt.Sprintf("%s (auto)", a.Name)
	sess, err := r.sessions.Create(name, projectPath, a.Prompt, false, session.CreateOpts{
		AutomationID: a.ID,
	})
	if err != nil {
		run.Status = RunError
		run.Message = err.Error()
		r.logger.Error("automation failed: create session",
			"automationId", a.ID, "name", a.Name, "error", err)
		return r.record(run)
	}

	// Manager.Create inserts the session with status idle; promote it to
	// working so the multi-run guard above can detect it as still active
	// (same pattern as the send endpoint after SendKeys). The turn-complete
	// callback wired in Manager.Create sets it back to idle when the CLI
	// finishes the turn.
	if err := r.sessions.UpdateStatus(sess.ID, session.StatusWorking); err != nil {
		r.logger.Warn("automation: failed to mark session as working; the multi-run guard may not engage for this run",
			"automationId", a.ID, "sessionId", sess.ID, "error", err)
	}

	run.Status = RunSuccess
	run.SessionID = sess.ID
	r.logger.Info("automation fired: session created",
		"automationId", a.ID, "name", a.Name, "sessionId", sess.ID, "projectPath", projectPath)
	return r.record(run)
}

// previousRunActive reports whether the session created by the most recent
// run of the automation is still in progress (working or waiting for input).
func (r *Runner) previousRunActive(automationID string) (string, bool) {
	prev, err := r.store.LatestSessionRun(automationID)
	if err != nil {
		r.logger.Warn("automation: query latest session run failed", "automationId", automationID, "error", err)
		return "", false
	}
	if prev == nil {
		return "", false
	}
	sess, err := r.sessions.Get(prev.SessionID)
	if err != nil {
		// Session was deleted or cannot be loaded: treat as not running.
		return "", false
	}
	if sess.Status == session.StatusWorking || sess.Status == session.StatusWaitingInput {
		return sess.ID, true
	}
	return "", false
}

func (r *Runner) record(run Run) *Run {
	saved, err := r.store.InsertRun(run)
	if err != nil {
		r.logger.Error("automation: record run failed", "automationId", run.AutomationID, "error", err)
		return &run
	}
	return saved
}
