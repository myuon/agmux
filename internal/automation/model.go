package automation

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// TriggerType identifies how an automation is scheduled.
type TriggerType string

const (
	// TriggerInterval fires at a fixed interval. TriggerValue is a Go
	// duration string (e.g. "30m", "1h").
	TriggerInterval TriggerType = "interval"
	// TriggerCron fires according to a standard 5-field cron expression
	// stored in TriggerValue (e.g. "0 9 * * 1-5").
	TriggerCron TriggerType = "cron"
	// TriggerQueue fires while the automation has pending loop tasks and the
	// previous run has finished. TriggerValue is the wait time after the
	// previous run (Go duration, empty = 0s). Requires the "loops"
	// experimental feature.
	TriggerQueue TriggerType = "queue"
)

// RunStatus is the outcome of a single automation firing.
type RunStatus string

const (
	RunSuccess RunStatus = "success" // session was created and the prompt was sent
	RunSkipped RunStatus = "skipped" // previous run was still working, firing skipped
	RunError   RunStatus = "error"   // session creation / prompt send failed
)

// Automation is a scheduled prompt that creates a new session when fired.
type Automation struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Prompt       string      `json:"prompt"`
	TriggerType  TriggerType `json:"triggerType"`
	TriggerValue string      `json:"triggerValue"`
	// ProjectPath is the project working directory the created session runs
	// in. Empty means the controller area (~/.agmux/controller).
	ProjectPath string `json:"projectPath,omitempty"`
	// Model is the model passed to the created session. Empty means the
	// provider default.
	Model   string `json:"model,omitempty"`
	Enabled bool   `json:"enabled"`
	// MaxRuns disables the automation once this many successful runs have
	// been recorded. 0 = unlimited.
	MaxRuns int `json:"maxRuns,omitempty"`
	// Until disables the automation once this time has passed. nil = no
	// deadline.
	Until *time.Time `json:"until,omitempty"`
	// WatchMetric is the key of the loop_report metric watched for
	// stagnation and used to color the dashboard. Empty = none.
	WatchMetric string `json:"watchMetric,omitempty"`
	// WatchDirection says which direction of WatchMetric is better.
	WatchDirection WatchDirection `json:"watchDirection,omitempty"`
	// CreatedBySessionID is the session (AGMUX_SESSION_ID) that created the
	// automation via the CLI / MCP / API. Empty when created from the UI.
	CreatedBySessionID string    `json:"createdBySessionId,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// WatchDirection tells which direction of the watched metric is an improvement.
type WatchDirection string

const (
	WatchHigher WatchDirection = "higher"
	WatchLower  WatchDirection = "lower"
)

// ValidateWatch checks the WatchMetric / WatchDirection combination.
func ValidateWatch(metric string, dir WatchDirection) error {
	if metric == "" {
		if dir != "" {
			return fmt.Errorf("watchDirection requires watchMetric")
		}
		return nil
	}
	if dir != WatchHigher && dir != WatchLower {
		return fmt.Errorf(`watchDirection must be "higher" or "lower" when watchMetric is set`)
	}
	return nil
}

// UsesLoopFeatures reports whether the automation relies on the "loops"
// experimental feature (queue trigger or safety valves).
func (a Automation) UsesLoopFeatures() bool {
	return a.TriggerType == TriggerQueue || a.MaxRuns > 0 || a.Until != nil || a.WatchMetric != ""
}

// Run is one entry in the execution history of an automation.
type Run struct {
	ID           int64     `json:"id"`
	AutomationID string    `json:"automationId"`
	FiredAt      time.Time `json:"firedAt"`
	// SessionID is the session created by this run. Empty for skipped /
	// error runs.
	SessionID string    `json:"sessionId,omitempty"`
	Status    RunStatus `json:"status"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// PendingAtFire is the pending loop task count when the run fired. nil =
	// not recorded (loops off, or a run from before this was tracked). Only
	// used on insert; the watchdog reads it via Store.WatchRuns.
	PendingAtFire *int `json:"-"`
}

// ValidateTrigger checks that the trigger type / value combination is parseable.
func ValidateTrigger(triggerType TriggerType, triggerValue string) error {
	switch triggerType {
	case TriggerInterval:
		d, err := time.ParseDuration(triggerValue)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", triggerValue, err)
		}
		if d <= 0 {
			return fmt.Errorf("interval must be positive, got %q", triggerValue)
		}
		return nil
	case TriggerCron:
		if _, err := cron.ParseStandard(triggerValue); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", triggerValue, err)
		}
		return nil
	case TriggerQueue:
		_, err := QueueWait(triggerValue)
		return err
	default:
		return fmt.Errorf("unknown trigger type %q", triggerType)
	}
}

// QueueWait parses the TriggerValue of a queue trigger (empty = 0s).
func QueueWait(triggerValue string) (time.Duration, error) {
	if triggerValue == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(triggerValue)
	if err != nil {
		return 0, fmt.Errorf("invalid queue wait %q: %w", triggerValue, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("queue wait must not be negative, got %q", triggerValue)
	}
	return d, nil
}

// NextFireTime returns the next time the automation should fire strictly
// after the given reference time (the last fire time, or the creation time
// when the automation has never fired).
func NextFireTime(a Automation, after time.Time) (time.Time, error) {
	switch a.TriggerType {
	case TriggerInterval:
		d, err := time.ParseDuration(a.TriggerValue)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid interval %q: %w", a.TriggerValue, err)
		}
		if d <= 0 {
			return time.Time{}, fmt.Errorf("interval must be positive, got %q", a.TriggerValue)
		}
		return after.Add(d), nil
	case TriggerCron:
		sched, err := cron.ParseStandard(a.TriggerValue)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", a.TriggerValue, err)
		}
		return sched.Next(after), nil
	case TriggerQueue:
		// Queue automations are not time-driven; the scheduler decides based
		// on pending tasks. This only reports the end of the wait window.
		d, err := QueueWait(a.TriggerValue)
		if err != nil {
			return time.Time{}, err
		}
		return after.Add(d), nil
	default:
		return time.Time{}, fmt.Errorf("unknown trigger type %q", a.TriggerType)
	}
}

// IsDue reports whether the automation should fire at `now`, given the last
// fire time (zero value means never fired; the automation's CreatedAt is used
// as the baseline so a fresh automation does not fire immediately).
func IsDue(a Automation, lastFiredAt, now time.Time) (bool, error) {
	base := lastFiredAt
	if base.IsZero() {
		base = a.CreatedAt
	}
	next, err := NextFireTime(a, base)
	if err != nil {
		return false, err
	}
	return !next.After(now), nil
}
