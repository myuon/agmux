package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// TestBackgroundTaskStore_DismissedTaskIsNotResurrected verifies that a
// delayed task_progress event for a dismissed task does not bring the row
// back. The frontend / API List() must continue to hide the task.
func TestBackgroundTaskStore_DismissedTaskIsNotResurrected(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))
	require.NoError(t, store.Dismiss("s", "t"))

	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 0)

	// Late-arriving task_progress event for the same task_id must not
	// resurrect the dismissed row.
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_progress","task_id":"t","description":"late","last_tool_name":"Bash"}`)))
	tasks, err = store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0, "dismissed task must not be resurrected by late task_progress")

	// And a fresh task_started for the same id is also blocked (tombstone).
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))
	tasks, err = store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0, "dismissed task must stay hidden even when re-started")
}

// TestBackgroundTaskStore_DismissBeforeAnyTaskStarted creates a tombstone
// for an unknown task_id and verifies that a subsequent task_started /
// task_progress for that id is still suppressed.
func TestBackgroundTaskStore_DismissBeforeAnyTaskStarted(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.Dismiss("s", "t"))
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))

	tasks, err := store.List("s")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

// TestBackgroundTaskStore_ConcurrentApplyLine simulates two goroutines
// (e.g. RebuildFromJSONL and a live onNewLines callback) appending
// task_progress entries concurrently. Without serialization the
// tool_call_history would lose entries via read-modify-write race.
func TestBackgroundTaskStore_ConcurrentApplyLine(t *testing.T) {
	store := newTestBgStore(t)

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent"}`)))

	const N = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			line := fmt.Sprintf(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"A%d","description":"d"}`, i)
			_ = store.ApplyLine("s", []byte(line))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			line := fmt.Sprintf(`{"type":"system","subtype":"task_progress","task_id":"t","last_tool_name":"B%d","description":"d"}`, i)
			_ = store.ApplyLine("s", []byte(line))
		}
	}()
	wg.Wait()

	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	// Expect exactly 2*N appended history entries (every line has a unique
	// last_tool_name so dedup never drops anything). Without the mutex this
	// would frequently come out short.
	assert.Equal(t, 2*N, len(tasks[0].ToolCallHistory),
		"expected %d history entries, got %d", 2*N, len(tasks[0].ToolCallHistory))
}

// TestBackgroundTaskStore_RebuildFromJSONL writes a JSONL file with
// task events, runs RebuildFromJSONL with various clearOffset values,
// and asserts the resulting DB state matches the replayed segment.
func TestBackgroundTaskStore_RebuildFromJSONL(t *testing.T) {
	store := newTestBgStore(t)

	// RebuildFromJSONL reads from db.StreamsDir(), so point HOME at the
	// test temp dir to isolate file IO.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	streamsDir, err := db.StreamsDir()
	require.NoError(t, err)
	jsonlPath := filepath.Join(streamsDir, "sess.jsonl")

	earlyLine := `{"type":"system","subtype":"task_started","task_id":"t-early","task_type":"local_agent","description":"early"}` + "\n"
	lateStarted := `{"type":"system","subtype":"task_started","task_id":"t-late","task_type":"local_agent","description":"late"}` + "\n"
	lateProgress := `{"type":"system","subtype":"task_progress","task_id":"t-late","last_tool_name":"Bash","description":"step"}` + "\n"

	contents := []byte(earlyLine + lateStarted + lateProgress)
	require.NoError(t, os.WriteFile(jsonlPath, contents, 0o644))

	// Full replay (offset 0) should yield two tasks.
	require.NoError(t, store.RebuildFromJSONL("sess", 0))
	tasks, err := store.List("sess")
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// Replay starting from after the early line should yield only the late task.
	offset := int64(len(earlyLine))
	require.NoError(t, store.RebuildFromJSONL("sess", offset))
	tasks, err = store.List("sess")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t-late", tasks[0].TaskID)
	require.Len(t, tasks[0].ToolCallHistory, 1)
	assert.Equal(t, "Bash", tasks[0].ToolCallHistory[0].ToolName)

	// Missing JSONL file must not error.
	require.NoError(t, os.Remove(jsonlPath))
	require.NoError(t, store.RebuildFromJSONL("sess", 0))
	tasks, err = store.List("sess")
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

// TestBackgroundTaskStore_PendingToolUseInputsCap verifies the 256-entry
// cap on the pendingToolUseInputs map keeps the oldest entries from
// growing unbounded.
func TestBackgroundTaskStore_PendingToolUseInputsCap(t *testing.T) {
	store := newTestBgStore(t)

	for i := 0; i < 300; i++ {
		line := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"id-%d","name":"Tool","input":{"k":%d}}]}}`, i, i)
		require.NoError(t, store.ApplyLine("s", []byte(line)))
	}

	store.mu.Lock()
	got := len(store.pendingToolUseInputs["s"])
	store.mu.Unlock()
	assert.LessOrEqual(t, got, 256, "pending tool_use map must be capped to 256 entries")
	assert.Greater(t, got, 0)
}

// TestBackgroundTaskStore_NullToolUseInputIsIgnored mirrors the frontend
// behavior of skipping tool_use blocks whose input is null.
func TestBackgroundTaskStore_NullToolUseInputIsIgnored(t *testing.T) {
	store := newTestBgStore(t)

	// tool_use with input: null should not register a pending input.
	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu","name":"Agent","input":null}]}}`)))

	require.NoError(t, store.ApplyLine("s", []byte(`{"type":"system","subtype":"task_started","task_id":"t","task_type":"local_agent","tool_use_id":"tu"}`)))
	tasks, err := store.List("s")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Empty(t, tasks[0].LastToolName, "null input must not resolve to a lastToolName")
	assert.Nil(t, tasks[0].LastToolInput)
}
