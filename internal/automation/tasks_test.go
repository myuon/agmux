package automation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTaskTestAutomation(t *testing.T) (*Store, *Automation) {
	t.Helper()
	store := newTestStore(t)
	a, err := store.Create(CreateParams{
		Name: "loop", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true,
	})
	require.NoError(t, err)
	return store, a
}

func TestTaskNextDoneFlow(t *testing.T) {
	store, a := newTaskTestAutomation(t)

	// Empty list: nothing to take.
	got, err := store.NextTask(a.ID, nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	t1, err := store.AddTask(a.ID, "first", "body", CreatedByUser)
	require.NoError(t, err)
	t2, err := store.AddTask(a.ID, "second", "", CreatedByAgent)
	require.NoError(t, err)
	assert.Equal(t, TaskPending, t1.Status)
	assert.Equal(t, CreatedByAgent, t2.CreatedBy)

	run, err := store.InsertRun(Run{AutomationID: a.ID, FiredAt: time.Now(), SessionID: "s1", Status: RunSuccess})
	require.NoError(t, err)

	// Oldest pending comes first and is linked to the run.
	next, err := store.NextTask(a.ID, &run.ID)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, t1.ID, next.ID)
	assert.Equal(t, TaskInProgress, next.Status)
	require.NotNil(t, next.RunID)
	assert.Equal(t, run.ID, *next.RunID)

	// The in_progress task is not handed out twice.
	next2, err := store.NextTask(a.ID, &run.ID)
	require.NoError(t, err)
	require.NotNil(t, next2)
	assert.Equal(t, t2.ID, next2.ID)

	done, err := store.FinishTask(a.ID, t1.ID, TaskDone, "ok")
	require.NoError(t, err)
	assert.Equal(t, TaskDone, done.Status)
	assert.Equal(t, "ok", done.Result)

	_, err = store.FinishTask(a.ID, t2.ID, TaskPending, "")
	assert.Error(t, err)

	// Other automations' tasks are not reachable.
	_, err = store.FinishTask("other", t2.ID, TaskDone, "")
	assert.Error(t, err)

	rr, err := store.RunForSession("s1")
	require.NoError(t, err)
	require.NotNil(t, rr)
	assert.Equal(t, run.ID, rr.ID)
}

func TestReclaimAbandonedTasks(t *testing.T) {
	store, a := newTaskTestAutomation(t)
	run, err := store.InsertRun(Run{AutomationID: a.ID, FiredAt: time.Now(), SessionID: "s1", Status: RunSuccess})
	require.NoError(t, err)
	task, err := store.AddTask(a.ID, "work", "", CreatedByUser)
	require.NoError(t, err)

	active := true
	isActive := func(string) bool { return active }

	_, err = store.NextTask(a.ID, &run.ID)
	require.NoError(t, err)

	// Session still active: left alone.
	n, err := store.ReclaimAbandonedTasks(isActive, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Session stopped without loop_task_done: back to pending, attempts++.
	active = false
	for attempt := 1; attempt < MaxTaskAttempts; attempt++ {
		n, err = store.ReclaimAbandonedTasks(isActive, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got, err := store.GetTask(a.ID, task.ID)
		require.NoError(t, err)
		assert.Equal(t, TaskPending, got.Status)
		assert.Equal(t, attempt, got.Attempts)
		assert.Nil(t, got.RunID)

		// Pending tasks are not touched again.
		n, err = store.ReclaimAbandonedTasks(isActive, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		_, err = store.NextTask(a.ID, &run.ID)
		require.NoError(t, err)
	}

	// The last attempt reaches the limit: failed.
	n, err = store.ReclaimAbandonedTasks(isActive, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	got, err := store.GetTask(a.ID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskFailed, got.Status)
	assert.Equal(t, MaxTaskAttempts, got.Attempts)
	assert.NotEmpty(t, got.Result)
}

func TestReclaimTasksWithoutRunUsesGrace(t *testing.T) {
	store, a := newTaskTestAutomation(t)
	task, err := store.AddTask(a.ID, "work", "", CreatedByUser)
	require.NoError(t, err)
	_, err = store.NextTask(a.ID, nil)
	require.NoError(t, err)

	n, err := store.ReclaimAbandonedTasks(func(string) bool { return false }, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "recent task without a run must not be reclaimed")

	n, err = store.ReclaimAbandonedTasks(func(string) bool { return false }, time.Now().Add(orphanGrace+time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	got, err := store.GetTask(a.ID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskPending, got.Status)
}

func TestUpdateAndDeleteTask(t *testing.T) {
	store, a := newTaskTestAutomation(t)
	task, err := store.AddTask(a.ID, "work", "", CreatedByUser)
	require.NoError(t, err)

	title := "renamed"
	st := TaskFailed
	got, err := store.UpdateTask(a.ID, task.ID, UpdateTaskParams{Title: &title, Status: &st})
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Title)
	assert.Equal(t, TaskFailed, got.Status)

	empty := " "
	_, err = store.UpdateTask(a.ID, task.ID, UpdateTaskParams{Title: &empty})
	assert.Error(t, err)

	pending := TaskPending
	got, err = store.UpdateTask(a.ID, task.ID, UpdateTaskParams{Status: &pending})
	require.NoError(t, err)
	assert.Equal(t, TaskPending, got.Status)

	require.NoError(t, store.DeleteTask(a.ID, task.ID))
	assert.Error(t, store.DeleteTask(a.ID, task.ID))

	_, err = store.AddTask(a.ID, "x", "", CreatedByUser)
	require.NoError(t, err)
	require.NoError(t, store.Delete(a.ID))
	_, err = store.AddTask(a.ID, "y", "", CreatedByUser)
	assert.Error(t, err)
}
