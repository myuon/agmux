package automation

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
)

// SessionCreator is the subset of session.SessionService the runner needs.
// session.Manager satisfies it.
type SessionCreator interface {
	Create(name, projectPath, prompt string, worktree bool, opts ...session.CreateOpts) (*session.Session, error)
	Get(id string) (*session.Session, error)
	UpdateStatus(id string, status session.Status) error
}

// QueuePromptSuffix is appended to the initial prompt of sessions started by
// a queue-trigger automation.
const QueuePromptSuffix = "\n\n---\nこのセッションはループの1回です。最初に loop_task_next でタスクを1件取り出し、それに取り組んでください。終わったら loop_task_done で結果（done/failed）を記録し、最後に loop_report で指標と要約を記録してください。作業中に見つけた別の作業は loop_task_add で残タスクに追加してください。loop_task_next が空を返したら何もせず終了してください。"

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
	if experimental.Enabled(FeatureLoops) {
		if n, err := r.store.CountPendingTasks(a.ID); err == nil {
			run.PendingAtFire = &n
		}
	}

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
	prompt := a.Prompt
	if a.TriggerType == TriggerQueue {
		prompt += QueuePromptSuffix
	}
	sess, err := r.sessions.Create(name, projectPath, prompt, false, session.CreateOpts{
		AutomationID: a.ID,
		Model:        a.Model,
	})
	if err != nil {
		run.Status = RunError
		run.Message = err.Error()
		r.logger.Error("automation failed: create session",
			"automationId", a.ID, "name", a.Name, "error", err)
		return r.record(run)
	}

	// Manager.Create already inserts the session as working when it hands an
	// initial prompt to the CLI (see #708); this call keeps the multi-run
	// guard above working for the remaining cases (empty prompt, or a
	// SessionService implementation that starts sessions idle). The
	// turn-complete callback wired in Manager.Create sets it back to idle when
	// the CLI finishes the turn.
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

// PreviousRunState reports whether the session of the most recent run is
// still active and when that run ended. The end time is the session's last
// update when it is later than the run's fired_at (fallback: fired_at).
func (r *Runner) PreviousRunState(automationID string) (active bool, finishedAt time.Time) {
	if _, act := r.previousRunActive(automationID); act {
		return true, time.Time{}
	}
	prev, err := r.store.LatestRun(automationID)
	if err != nil || prev == nil {
		return false, time.Time{}
	}
	finishedAt = prev.FiredAt
	if prev.SessionID != "" {
		if sess, err := r.sessions.Get(prev.SessionID); err == nil && sess.UpdatedAt.After(finishedAt) {
			finishedAt = sess.UpdatedAt
		}
	}
	return false, finishedAt
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

// SessionStopped reports whether the run session is no longer active (stopped
// or no longer loadable).
func (r *Runner) SessionStopped(sessionID string) bool {
	sess, err := r.sessions.Get(sessionID)
	if err != nil {
		return true
	}
	return sess.Status != session.StatusWorking && sess.Status != session.StatusWaitingInput
}

// ReclaimTasks returns tasks left in_progress by finished run sessions to the
// pending list (see Store.ReclaimAbandonedTasks).
func (r *Runner) ReclaimTasks(now time.Time) {
	n, err := r.store.ReclaimAbandonedTasks(func(sessionID string) bool {
		sess, err := r.sessions.Get(sessionID)
		if err != nil {
			return false
		}
		return sess.Status == session.StatusWorking || sess.Status == session.StatusWaitingInput
	}, now)
	if err != nil {
		r.logger.Warn("automation: reclaim abandoned tasks failed", "error", err)
		return
	}
	if n > 0 {
		r.logger.Info("automation: reclaimed abandoned loop tasks", "count", n)
	}
}

func (r *Runner) record(run Run) *Run {
	saved, err := r.store.InsertRun(run)
	if err != nil {
		r.logger.Error("automation: record run failed", "automationId", run.AutomationID, "error", err)
		return &run
	}
	return saved
}
