package automation

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewStore(sqlDB)
}

// fakeSessions is a SessionCreator test double. It records Create calls and
// serves Get from a status map.
type fakeSessions struct {
	mu        sync.Mutex
	created   []fakeCreateCall
	statuses  map[string]session.Status
	createErr error
}

type fakeCreateCall struct {
	name        string
	projectPath string
	prompt      string
	opts        session.CreateOpts
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{statuses: map[string]session.Status{}}
}

func (f *fakeSessions) Create(name, projectPath, prompt string, worktree bool, opts ...session.CreateOpts) (*session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	var o session.CreateOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	f.created = append(f.created, fakeCreateCall{name: name, projectPath: projectPath, prompt: prompt, opts: o})
	id := fmt.Sprintf("sess-%d", len(f.created))
	f.statuses[id] = session.StatusWorking
	return &session.Session{ID: id, Name: name, ProjectPath: projectPath, Status: session.StatusWorking}, nil
}

func (f *fakeSessions) Get(id string) (*session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return &session.Session{ID: id, Status: st}, nil
}

func (f *fakeSessions) setStatus(id string, st session.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = st
}

func newTestRunner(t *testing.T, store *Store, sessions *fakeSessions) *Runner {
	t.Helper()
	r := NewRunner(store, sessions, slog.Default())
	r.controllerDir = func() (string, error) { return "/tmp/controller-test", nil }
	return r
}

// --- trigger / due-time logic -----------------------------------------------

func TestValidateTrigger(t *testing.T) {
	assert.NoError(t, ValidateTrigger(TriggerInterval, "30m"))
	assert.NoError(t, ValidateTrigger(TriggerCron, "0 9 * * 1-5"))
	assert.Error(t, ValidateTrigger(TriggerInterval, "not-a-duration"))
	assert.Error(t, ValidateTrigger(TriggerInterval, "-5m"))
	assert.Error(t, ValidateTrigger(TriggerInterval, "0s"))
	assert.Error(t, ValidateTrigger(TriggerCron, "every day"))
	assert.Error(t, ValidateTrigger(TriggerType("unknown"), "30m"))
}

func TestIsDue_Interval(t *testing.T) {
	created := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	a := Automation{TriggerType: TriggerInterval, TriggerValue: "30m", CreatedAt: created}

	// Never fired: baseline is CreatedAt, so it does not fire immediately.
	due, err := IsDue(a, time.Time{}, created.Add(10*time.Minute))
	require.NoError(t, err)
	assert.False(t, due)

	due, err = IsDue(a, time.Time{}, created.Add(30*time.Minute))
	require.NoError(t, err)
	assert.True(t, due)

	// After a fire, the next fire is interval after the last fire.
	last := created.Add(30 * time.Minute)
	due, err = IsDue(a, last, last.Add(29*time.Minute))
	require.NoError(t, err)
	assert.False(t, due)

	due, err = IsDue(a, last, last.Add(31*time.Minute))
	require.NoError(t, err)
	assert.True(t, due)
}

func TestIsDue_Cron(t *testing.T) {
	created := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	a := Automation{TriggerType: TriggerCron, TriggerValue: "0 9 * * *", CreatedAt: created}

	// Before 09:00: not due.
	due, err := IsDue(a, time.Time{}, time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, due)

	// At 09:00: due.
	due, err = IsDue(a, time.Time{}, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.True(t, due)

	// Fired at 09:00: not due again until the next day.
	last := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	due, err = IsDue(a, last, time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, due)

	due, err = IsDue(a, last, time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.True(t, due)

	// Daemon down across several occurrences: fires (once) on the next check.
	due, err = IsDue(a, last, time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.True(t, due)
}

func TestIsDue_InvalidTrigger(t *testing.T) {
	a := Automation{TriggerType: TriggerInterval, TriggerValue: "bogus", CreatedAt: time.Now()}
	_, err := IsDue(a, time.Time{}, time.Now())
	assert.Error(t, err)
}

// --- store -------------------------------------------------------------------

func TestStore_CRUD(t *testing.T) {
	store := newTestStore(t)

	a, err := store.Create(CreateParams{
		Name:         "daily-report",
		Prompt:       "write the daily report",
		TriggerType:  TriggerCron,
		TriggerValue: "0 9 * * *",
		ProjectPath:  "/tmp/proj",
		Enabled:      true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, a.ID)

	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.Equal(t, "daily-report", got.Name)
	assert.Equal(t, TriggerCron, got.TriggerType)
	assert.Equal(t, "0 9 * * *", got.TriggerValue)
	assert.Equal(t, "/tmp/proj", got.ProjectPath)
	assert.True(t, got.Enabled)

	// Update
	updated, err := store.Update(a.ID, UpdateParams{
		Name:         "daily-report-v2",
		Prompt:       "write the daily report v2",
		TriggerType:  TriggerInterval,
		TriggerValue: "1h",
		ProjectPath:  "",
		Enabled:      false,
	})
	require.NoError(t, err)
	assert.Equal(t, "daily-report-v2", updated.Name)
	assert.Equal(t, TriggerInterval, updated.TriggerType)
	assert.Equal(t, "", updated.ProjectPath)
	assert.False(t, updated.Enabled)

	// SetEnabled
	require.NoError(t, store.SetEnabled(a.ID, true))
	got, err = store.Get(a.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)

	// List / ListEnabled
	all, err := store.List()
	require.NoError(t, err)
	assert.Len(t, all, 1)
	enabled, err := store.ListEnabled()
	require.NoError(t, err)
	assert.Len(t, enabled, 1)

	require.NoError(t, store.SetEnabled(a.ID, false))
	enabled, err = store.ListEnabled()
	require.NoError(t, err)
	assert.Len(t, enabled, 0)

	// Delete
	require.NoError(t, store.Delete(a.ID))
	_, err = store.Get(a.ID)
	assert.Error(t, err)
}

func TestStore_CreateValidatesTrigger(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Create(CreateParams{
		Name: "bad", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "bogus", Enabled: true,
	})
	assert.Error(t, err)
}

// --- runner ------------------------------------------------------------------

func TestRunner_CreatesSessionAndRecordsSuccess(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	r := newTestRunner(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "do the thing", TriggerType: TriggerInterval, TriggerValue: "30m",
		ProjectPath: "/tmp/proj", Enabled: true,
	})
	require.NoError(t, err)

	firedAt := time.Now()
	run := r.Run(*a, firedAt)
	assert.Equal(t, RunSuccess, run.Status)
	assert.Equal(t, "sess-1", run.SessionID)

	require.Len(t, sessions.created, 1)
	assert.Equal(t, "/tmp/proj", sessions.created[0].projectPath)
	assert.Equal(t, "do the thing", sessions.created[0].prompt)
	assert.Equal(t, a.ID, sessions.created[0].opts.AutomationID)

	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, RunSuccess, runs[0].Status)
	assert.Equal(t, "sess-1", runs[0].SessionID)
}

func TestRunner_NoProjectUsesControllerDir(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	r := newTestRunner(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	run := r.Run(*a, time.Now())
	assert.Equal(t, RunSuccess, run.Status)
	require.Len(t, sessions.created, 1)
	assert.Equal(t, "/tmp/controller-test", sessions.created[0].projectPath)
}

func TestRunner_SkipsWhilePreviousRunActive(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	r := newTestRunner(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	// First fire: creates sess-1 which stays in "working".
	run1 := r.Run(*a, time.Now())
	require.Equal(t, RunSuccess, run1.Status)

	// Second fire while sess-1 is still working: skipped, no new session.
	run2 := r.Run(*a, time.Now())
	assert.Equal(t, RunSkipped, run2.Status)
	assert.Empty(t, run2.SessionID)
	assert.Contains(t, run2.Message, "sess-1")
	assert.Len(t, sessions.created, 1)

	// waiting_input also counts as active.
	sessions.setStatus("sess-1", session.StatusWaitingInput)
	run3 := r.Run(*a, time.Now())
	assert.Equal(t, RunSkipped, run3.Status)

	// Once the previous session is idle, the next fire goes through.
	sessions.setStatus("sess-1", session.StatusIdle)
	run4 := r.Run(*a, time.Now())
	assert.Equal(t, RunSuccess, run4.Status)
	assert.Equal(t, "sess-2", run4.SessionID)
	assert.Len(t, sessions.created, 2)

	// History records every firing including the skips.
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 4)
	statuses := []RunStatus{runs[3].Status, runs[2].Status, runs[1].Status, runs[0].Status}
	assert.Equal(t, []RunStatus{RunSuccess, RunSkipped, RunSkipped, RunSuccess}, statuses)
}

func TestRunner_PreviousSessionDeletedDoesNotBlock(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	r := newTestRunner(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	run1 := r.Run(*a, time.Now())
	require.Equal(t, RunSuccess, run1.Status)

	// Session deleted: Get fails, runner must not treat it as active.
	sessions.mu.Lock()
	delete(sessions.statuses, "sess-1")
	sessions.mu.Unlock()

	run2 := r.Run(*a, time.Now())
	assert.Equal(t, RunSuccess, run2.Status)
}

func TestRunner_RecordsError(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sessions.createErr = fmt.Errorf("spawn failed")
	r := newTestRunner(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	run := r.Run(*a, time.Now())
	assert.Equal(t, RunError, run.Status)
	assert.Contains(t, run.Message, "spawn failed")

	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, RunError, runs[0].Status)
}

// --- scheduler ---------------------------------------------------------------

func newTestScheduler(t *testing.T, store *Store, sessions *fakeSessions) *Scheduler {
	t.Helper()
	return NewScheduler(store, newTestRunner(t, store, sessions), slog.Default())
}

func TestScheduler_Tick_FiresIntervalWhenDue(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m",
		ProjectPath: "/tmp/proj", Enabled: true,
	})
	require.NoError(t, err)

	// Not yet due.
	sched.Tick(a.CreatedAt.Add(10 * time.Minute))
	assert.Len(t, sessions.created, 0)

	// Due: fires and records a run.
	sched.Tick(a.CreatedAt.Add(31 * time.Minute))
	assert.Len(t, sessions.created, 1)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, RunSuccess, runs[0].Status)

	// Same tick time again: the recorded run prevents a double fire.
	sched.Tick(a.CreatedAt.Add(31 * time.Minute))
	assert.Len(t, sessions.created, 1)
}

func TestScheduler_Tick_FiresCronWhenDue(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)

	a, err := store.Create(CreateParams{
		// Every minute, so the next occurrence after CreatedAt is < 1 minute away.
		Name: "auto-cron", Prompt: "p", TriggerType: TriggerCron, TriggerValue: "* * * * *",
		Enabled: true,
	})
	require.NoError(t, err)

	// Before the next minute boundary relative to creation: not due.
	sched.Tick(a.CreatedAt)
	assert.Len(t, sessions.created, 0)

	// Two minutes later a minute boundary has definitely passed: due.
	sched.Tick(a.CreatedAt.Add(2 * time.Minute))
	assert.Len(t, sessions.created, 1)
}

func TestScheduler_Tick_IgnoresDisabled(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: false,
	})
	require.NoError(t, err)

	sched.Tick(a.CreatedAt.Add(2 * time.Hour))
	assert.Len(t, sessions.created, 0)
}

func TestScheduler_Tick_SkipRecordedAdvancesSchedule(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	// First fire creates sess-1 (stays working).
	sched.Tick(a.CreatedAt.Add(30 * time.Minute))
	require.Len(t, sessions.created, 1)

	// Next interval boundary while sess-1 is still working: skipped.
	sched.Tick(a.CreatedAt.Add(60 * time.Minute))
	assert.Len(t, sessions.created, 1)

	// A skipped run also counts as fired: the very next tick (still within the
	// interval after the skip) must not fire again.
	sched.Tick(a.CreatedAt.Add(61 * time.Minute))
	assert.Len(t, sessions.created, 1)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, RunSkipped, runs[0].Status)
	assert.Equal(t, RunSuccess, runs[1].Status)

	// Session finished: the next interval after the skip fires again.
	sessions.setStatus("sess-1", session.StatusIdle)
	sched.Tick(a.CreatedAt.Add(90 * time.Minute))
	assert.Len(t, sessions.created, 2)
}

func TestScheduler_Tick_InvalidTriggerDoesNotPanic(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)

	// Bypass Create validation by writing an invalid trigger directly.
	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)
	_, err = store.db.Exec(`UPDATE automations SET trigger_value = 'bogus' WHERE id = ?`, a.ID)
	require.NoError(t, err)

	sched.Tick(time.Now().Add(24 * time.Hour))
	assert.Len(t, sessions.created, 0)
}

func TestScheduler_StartStop(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	sched.SetTickInterval(10 * time.Millisecond)

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1ms", Enabled: true,
	})
	require.NoError(t, err)
	_ = a

	sched.Start()
	// Wait until the loop has fired at least once.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sessions.mu.Lock()
		n := len(sessions.created)
		sessions.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sched.Stop()

	sessions.mu.Lock()
	n := len(sessions.created)
	sessions.mu.Unlock()
	assert.GreaterOrEqual(t, n, 1)

	// Stop is idempotent.
	sched.Stop()
}
