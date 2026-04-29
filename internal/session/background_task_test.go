package session

import (
	"path/filepath"
	"testing"

	"github.com/myuon/agmux/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBgStore(t *testing.T) *BackgroundTaskStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDB, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewBackgroundTaskStore(sqlDB)
}

func TestBackgroundTaskStore_TaskStartedAddsTask(t *testing.T) {
	store := newTestBgStore(t)

	// task_started system event
	line := `{"type":"system","subtype":"task_started","task_id":"t1","task_type":"local_agent","agent_id":"a1","description":"do thing","timestamp":"2025-01-01T00:00:00Z"}`
	require.NoError(t, store.ApplyLine("sess-1", []byte(line)))

	tasks, err := store.List("sess-1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t1", tasks[0].TaskID)
	assert.Equal(t, "local_agent", tasks[0].TaskType)
	assert.Equal(t, "a1", tasks[0].AgentID)
	assert.Equal(t, "do thing", tasks[0].Description)
	assert.Equal(t, "2025-01-01T00:00:00Z", tasks[0].StartedAt)
}

func TestBackgroundTaskStore_TaskNotificationRemovesTask(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t1","task_type":"local_bash"}`)))
	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_notification","task_id":"t1","status":"completed"}`)))
	tasks, err = store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

func TestBackgroundTaskStore_TaskProgressUpdatesFields(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent","timestamp":"2025-01-01T00:00:00Z"}`)))

	progress := `{"type":"system","subtype":"task_progress","task_id":"t","description":"phase 2","last_tool_name":"Bash","last_tool_input":{"command":"ls"},"output":"some","usage":{"input_tokens":100,"output_tokens":50},"timestamp":"2025-01-01T00:01:00Z"}`
	require.NoError(t, store.ApplyLine("s", []byte(progress)))

	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	task := tasks[0]
	assert.Equal(t, "phase 2", task.Description)
	assert.Equal(t, "Bash", task.LastToolName)
	assert.Equal(t, "some", task.Output)
	require.NotNil(t, task.Usage)
	require.NotNil(t, task.Usage.InputTokens)
	assert.Equal(t, 100, *task.Usage.InputTokens)
	require.NotNil(t, task.Usage.OutputTokens)
	assert.Equal(t, 50, *task.Usage.OutputTokens)
	require.Len(t, task.ToolCallHistory, 1)
	assert.Equal(t, "Bash", task.ToolCallHistory[0].ToolName)
}

func TestBackgroundTaskStore_TaskProgressDeduplicatesHistory(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))

	// Same tool name and description -> no new history entry
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"Read","description":"d1"}`)))
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"Read","description":"d1"}`)))
	// Same tool name but different description -> new entry
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"Read","description":"d2"}`)))
	// Different tool name -> new entry
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"Bash"}`)))

	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, []BackgroundTaskToolCall{
		{ToolName: "Read", Description: "d1"},
		{ToolName: "Read", Description: "d2"},
		{ToolName: "Bash"},
	}, tasks[0].ToolCallHistory)
}

func TestBackgroundTaskStore_TaskStartedResolvesToolUseInput(t *testing.T) {
	store := newTestBgStore(t)

	// First an assistant message with a tool_use of name "Agent"
	assistant := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Agent","input":{"prompt":"hi"}}]}}`
	require.NoError(t, store.ApplyLine("s", []byte(assistant)))

	// Then a task_started referring to that tool_use_id
	started := `{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent","tool_use_id":"tu1"}`
	require.NoError(t, store.ApplyLine("s", []byte(started)))

	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Agent", tasks[0].LastToolName)
	require.NotNil(t, tasks[0].LastToolInput)
}

func TestBackgroundTaskStore_TaskStopRemovesTask(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))

	// Assistant calls TaskStop tool
	stop := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu","name":"TaskStop","input":{"task_id":"t"}}]}}`
	require.NoError(t, store.ApplyLine("s", []byte(stop)))

	tasks, err := store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

func TestBackgroundTaskStore_DismissRemovesTask(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))
	require.NoError(t, store.Dismiss("s", "t"))

	tasks, err := store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

func TestBackgroundTaskStore_ListIsEmptyForUnknownSession(t *testing.T) {
	store := newTestBgStore(t)
	tasks, err := store.List("missing")
	require.NoError(t, err)
	assert.NotNil(t, tasks)
	assert.Len(t, tasks, 0)
}

func TestBackgroundTaskStore_ClearSessionDropsAllTasks(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t1","task_type":"local_agent"}`)))
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t2","task_type":"local_agent"}`)))

	require.NoError(t, store.ClearSession("s"))

	tasks, err := store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

func TestBackgroundTaskStore_NonTaskLinesAreIgnored(t *testing.T) {
	store := newTestBgStore(t)

	// Various non-task lines should not error and not produce rows.
	for _, line := range []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"system","subtype":"init"}`,
		`{"type":"result","subtype":"success"}`,
		`not json`,
		``,
	} {
		require.NoError(t, store.ApplyLine("s", []byte(line)))
	}
	tasks, err := store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}
