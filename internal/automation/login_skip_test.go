package automation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduler_LoginRequired_SkipsAndRecords(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	required := true
	sched.LoginRequired = func() bool { return required }

	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)

	// Not due yet: nothing recorded.
	sched.Tick(a.CreatedAt.Add(10 * time.Minute))
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	assert.Len(t, runs, 0)

	// Due while login is required: no session, a skipped run with the reason.
	sched.Tick(a.CreatedAt.Add(31 * time.Minute))
	assert.Len(t, sessions.created, 0)
	runs, err = store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, RunSkipped, runs[0].Status)
	assert.Equal(t, LoginSkipMessage, runs[0].Message)

	// Same tick again: the skip counts as a fire, no duplicate.
	sched.Tick(a.CreatedAt.Add(32 * time.Minute))
	runs, _ = store.ListRuns(a.ID, 10)
	assert.Len(t, runs, 1)

	// Login restored: fires on the next due time.
	required = false
	sched.Tick(a.CreatedAt.Add(62 * time.Minute))
	assert.Len(t, sessions.created, 1)
}

func TestScheduler_LoginRequired_QueueRecordsOnce(t *testing.T) {
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	sched.LoginRequired = func() bool { return true }
	a := newQueueAutomation(t, store, "", nil)
	_, err := store.AddTask(a.ID, "t1", "", "user")
	require.NoError(t, err)
	now := time.Now()

	for i := 0; i < 5; i++ {
		sched.Tick(now.Add(time.Duration(i) * time.Minute))
	}
	assert.Len(t, sessions.created, 0)
	runs, err := store.ListRuns(a.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, LoginSkipMessage, runs[0].Message)
}

func TestScheduler_LoginRequiredNil_FiresNormally(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	sched.LoginRequired = func() bool { return false }
	a, err := store.Create(CreateParams{
		Name: "auto1", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "30m", Enabled: true,
	})
	require.NoError(t, err)
	sched.Tick(a.CreatedAt.Add(31 * time.Minute))
	assert.Len(t, sessions.created, 1)
}
