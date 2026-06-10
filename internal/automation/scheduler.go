package automation

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DefaultTickInterval is how often the scheduler checks for due automations.
// Cron expressions have minute resolution, so 30s keeps firing latency low
// without busy-polling SQLite.
const DefaultTickInterval = 30 * time.Second

// runner abstracts Runner for tests.
type runner interface {
	Run(a Automation, firedAt time.Time) *Run
}

// Scheduler periodically checks enabled automations and fires the ones that
// are due. The last fire time is derived from the run history (skipped and
// error runs count as fired), which makes the scheduler restart-safe: after
// a daemon restart a missed schedule fires at most once.
type Scheduler struct {
	store    *Store
	runner   runner
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time

	mu     sync.Mutex
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewScheduler creates a scheduler with the default tick interval.
func NewScheduler(store *Store, r *Runner, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:    store,
		runner:   r,
		logger:   logger.With("component", "automation_scheduler"),
		interval: DefaultTickInterval,
		now:      time.Now,
	}
}

// SetTickInterval overrides the tick interval. Must be called before Start.
func (s *Scheduler) SetTickInterval(d time.Duration) {
	if d > 0 {
		s.interval = d
	}
}

// Start launches the scheduler loop in a goroutine. Calling Start on a
// running scheduler is a no-op.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		return
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.logger.Info("automation scheduler started", "tickInterval", s.interval)

	go func(stopCh, doneCh chan struct{}) {
		defer close(doneCh)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				s.Tick(s.now())
			}
		}
	}(s.stopCh, s.doneCh)
}

// Stop terminates the scheduler loop and waits for it to exit.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	stopCh, doneCh := s.stopCh, s.doneCh
	s.stopCh, s.doneCh = nil, nil
	s.mu.Unlock()
	if stopCh == nil {
		return
	}
	close(stopCh)
	<-doneCh
	s.logger.Info("automation scheduler stopped")
}

// Tick checks all enabled automations and fires the due ones. Exposed for
// tests; the Start loop calls it on every tick.
func (s *Scheduler) Tick(now time.Time) {
	automations, err := s.store.ListEnabled()
	if err != nil {
		s.logger.Error("automation scheduler: list enabled failed", "error", err)
		return
	}
	for _, a := range automations {
		due, err := s.isDue(a, now)
		if err != nil {
			// The wrapped error explains the cause (query latest run failed
			// vs. invalid trigger definition).
			s.logger.Warn("automation scheduler: due check failed, skipping",
				"automationId", a.ID, "name", a.Name,
				"triggerType", a.TriggerType, "triggerValue", a.TriggerValue, "error", err)
			continue
		}
		if !due {
			continue
		}
		// Runs are executed synchronously: the runner records the outcome
		// (success / skipped / error) before the next automation is checked,
		// so a single tick can never double-fire the same automation.
		s.runner.Run(a, now)
	}
}

func (s *Scheduler) isDue(a Automation, now time.Time) (bool, error) {
	last, err := s.store.LatestRun(a.ID)
	if err != nil {
		return false, fmt.Errorf("query latest run: %w", err)
	}
	var lastFiredAt time.Time
	if last != nil {
		lastFiredAt = last.FiredAt
	}
	due, err := IsDue(a, lastFiredAt, now)
	if err != nil {
		return false, fmt.Errorf("invalid trigger: %w", err)
	}
	return due, nil
}
