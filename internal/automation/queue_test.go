package automation

import (
	"testing"
	"time"

	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enableLoops(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, experimental.Set(FeatureLoops, true))
}

func newQueueAutomation(t *testing.T, store *Store, wait string, mod func(*CreateParams)) *Automation {
	t.Helper()
	p := CreateParams{Name: "q", Prompt: "do it", TriggerType: TriggerQueue, TriggerValue: wait, Enabled: true}
	if mod != nil {
		mod(&p)
	}
	a, err := store.Create(p)
	require.NoError(t, err)
	return a
}

func TestValidateTrigger_Queue(t *testing.T) {
	assert.NoError(t, ValidateTrigger(TriggerQueue, ""))
	assert.NoError(t, ValidateTrigger(TriggerQueue, "10m"))
	assert.Error(t, ValidateTrigger(TriggerQueue, "abc"))
	assert.Error(t, ValidateTrigger(TriggerQueue, "-1m"))
}

func TestScheduler_Queue_FiresWhilePendingAndPreviousFinished(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	a := newQueueAutomation(t, store, "", nil)
	now := time.Now()

	// pending 0: nothing happens and the automation stays enabled.
	sched.Tick(now)
	assert.Len(t, sessions.created, 0)
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)

	// Task added: fires, and the prompt carries the loop instructions.
	_, err = store.AddTask(a.ID, "t1", "", "user")
	require.NoError(t, err)
	sched.Tick(now.Add(time.Second))
	require.Len(t, sessions.created, 1)
	assert.Contains(t, sessions.created[0].prompt, "do it")
	assert.Contains(t, sessions.created[0].prompt, "loop_task_next")

	// Previous session still working: no new run, and no skipped run recorded.
	sched.Tick(now.Add(2 * time.Second))
	assert.Len(t, sessions.created, 1)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	assert.Len(t, runs, 1)

	// Previous session finished and still pending: next run starts.
	sessions.setStatus("sess-1", session.StatusIdle)
	sched.Tick(now.Add(3 * time.Second))
	assert.Len(t, sessions.created, 2)
}

func TestScheduler_Queue_WaitTime(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	a := newQueueAutomation(t, store, "10m", nil)
	_, err := store.AddTask(a.ID, "t1", "", "user")
	require.NoError(t, err)
	now := time.Now()

	sched.Tick(now)
	require.Len(t, sessions.created, 1)
	sessions.setStatus("sess-1", session.StatusIdle)

	sched.Tick(now.Add(5 * time.Minute))
	assert.Len(t, sessions.created, 1)
	sched.Tick(now.Add(11 * time.Minute))
	assert.Len(t, sessions.created, 2)
}

func TestScheduler_Queue_LoopsOffDoesNotFire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	a := newQueueAutomation(t, store, "", nil)
	_, err := store.AddTask(a.ID, "t1", "", "user")
	require.NoError(t, err)

	sched.Tick(time.Now())
	assert.Len(t, sessions.created, 0)
}

func TestScheduler_MaxRunsDisables(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	a := newQueueAutomation(t, store, "", func(p *CreateParams) { p.MaxRuns = 2 })
	for i := 0; i < 5; i++ {
		_, err := store.AddTask(a.ID, "t", "", "user")
		require.NoError(t, err)
	}
	now := time.Now()
	for i := 0; i < 4; i++ {
		sched.Tick(now.Add(time.Duration(i) * time.Second))
		sessions.setStatus("sess-1", session.StatusIdle)
		sessions.setStatus("sess-2", session.StatusIdle)
	}
	assert.Len(t, sessions.created, 2)
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	assert.Equal(t, RunSkipped, runs[0].Status)
	assert.Contains(t, runs[0].Message, "max runs")
}

func TestScheduler_UntilDisables(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	now := time.Now()
	until := now.Add(time.Hour)
	// Interval automations are covered by the safety valves too.
	a, err := store.Create(CreateParams{
		Name: "i", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "10m", Enabled: true, Until: &until,
	})
	require.NoError(t, err)

	sched.Tick(a.CreatedAt.Add(11 * time.Minute))
	assert.Len(t, sessions.created, 1)

	sched.Tick(now.Add(2 * time.Hour))
	assert.Len(t, sessions.created, 1)
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	require.NotNil(t, got.Until)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	assert.Contains(t, runs[0].Message, "deadline")
}

func TestScheduler_LimitsIgnoredWhenLoopsOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	until := time.Now().Add(-time.Hour)
	a, err := store.Create(CreateParams{
		Name: "i", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "10m", Enabled: true, Until: &until,
	})
	require.NoError(t, err)
	sched.Tick(a.CreatedAt.Add(11 * time.Minute))
	assert.Len(t, sessions.created, 1)
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

func TestStore_UpdateKeepsLimitsWhenOmitted(t *testing.T) {
	store := newTestStore(t)
	until := time.Now().Add(time.Hour)
	a, err := store.Create(CreateParams{
		Name: "i", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "10m", MaxRuns: 3, Until: &until,
	})
	require.NoError(t, err)
	up := UpdateParams{Name: "i2", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "10m"}
	got, err := store.Update(a.ID, up)
	require.NoError(t, err)
	assert.Equal(t, 3, got.MaxRuns)
	require.NotNil(t, got.Until)

	zero := 0
	up.MaxRuns, up.ClearUntil = &zero, true
	got, err = store.Update(a.ID, up)
	require.NoError(t, err)
	assert.Equal(t, 0, got.MaxRuns)
	assert.Nil(t, got.Until)
}

func TestScheduler_MaxRunsCallsOnLimitStop(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	var msgs []string
	sched.OnLimitStop = func(a Automation, message string) { msgs = append(msgs, message) }
	a := newQueueAutomation(t, store, "", func(p *CreateParams) { p.MaxRuns = 1 })
	_, err := store.AddTask(a.ID, "t", "", "user")
	require.NoError(t, err)
	now := time.Now()
	for i := 0; i < 3; i++ {
		sched.Tick(now.Add(time.Duration(i) * time.Second))
		sessions.setStatus("sess-1", session.StatusIdle)
	}
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "安全弁")
	assert.Contains(t, msgs[0], "max runs")
}
