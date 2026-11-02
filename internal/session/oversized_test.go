package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/db"
)

// replace feeds one (small-limit) oversized line followed by a normal line
// through StreamLineReader and returns the replacement as a generic map.
func replaceLine(t *testing.T, line string) map[string]any {
	t.Helper()
	in := line + "\n" + `{"type":"system","subtype":"after"}` + "\n"
	r := newStreamLineReaderLimit(strings.NewReader(in), "sid", "test", 1000)
	b, over, err := r.Next()
	require.NoError(t, err)
	require.True(t, over, "line should be oversized")
	require.True(t, json.Valid(b), "replacement must be valid JSON: %s", b)
	require.Less(t, len(b), 2048)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	// reader continues with the next line
	b2, over2, err := r.Next()
	require.NoError(t, err)
	require.False(t, over2)
	assert.Contains(t, string(b2), `"after"`)
	return m
}

func pad(n int) string { return strings.Repeat("A", n) }

// (a) a dropped result must still be a result so status detection settles.
func TestOversizedResultKeepsSubtype(t *testing.T) {
	// Real result lines put the huge "result" before "type"; subtype may come
	// before it.
	line := `{"is_error":false,"subtype":"success","result":"` + pad(200000) + `","session_id":"cli-1","type":"result","duration_ms":1}`
	m := replaceLine(t, line)
	assert.Equal(t, "result", m["type"])
	assert.Equal(t, "success", m["subtype"])
	assert.Equal(t, false, m["is_error"])
	assert.Equal(t, "cli-1", m["session_id"])
	assert.Contains(t, m["result"], "出力が大きすぎるため省略しました")
	assert.Contains(t, m["result"], "type=result")

	// the holder's turn-complete detection reads exactly these fields
	ev := parseStreamEvent([]byte(mustJSON(m)))
	assert.Equal(t, "result", ev.Type)
	assert.Equal(t, "success", ev.Subtype)
}

func TestOversizedErrorResultKeepsError(t *testing.T) {
	line := `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"` + pad(200000) + `"}`
	m := replaceLine(t, line)
	assert.Equal(t, "result", m["type"])
	assert.Equal(t, "error_max_turns", m["subtype"])
	assert.Equal(t, true, m["is_error"])
}

// A result whose subtype is not recoverable defaults to success so the
// session does not stay "working" forever.
func TestOversizedResultWithoutSubtypeDefaultsToSuccess(t *testing.T) {
	line := `{"result":"` + pad(200000) + `","type":"result"}`
	m := replaceLine(t, line)
	assert.Equal(t, "result", m["type"])
	assert.Equal(t, "success", m["subtype"])
}

// (b) a dropped tool_result: the tool call must get a result. tool_use_id comes
// after the huge content in Claude's real block order.
func TestOversizedToolResultKeepsToolUseID(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + pad(300000) +
		`","is_error":false,"tool_use_id":"toolu_abc"}]},"parent_tool_use_id":null,"session_id":"cli-1","uuid":"u","tool_use_result":"` + pad(1000) + `"}`
	m := replaceLine(t, line)
	assert.Equal(t, "user", m["type"])
	assert.Equal(t, "cli-1", m["session_id"])
	assert.Nil(t, m["parent_tool_use_id"])
	blocks := m["message"].(map[string]any)["content"].([]any)
	require.Len(t, blocks, 1)
	blk := blocks[0].(map[string]any)
	assert.Equal(t, "tool_result", blk["type"])
	assert.Equal(t, "toolu_abc", blk["tool_use_id"])
	assert.Equal(t, true, blk["is_error"])
	assert.Contains(t, blk["content"], "出力が大きすぎるため省略しました")
}

// (c) a subagent line keeps parent_tool_use_id even though it sits after the
// huge message, and so is picked up by FilterSubagentLines.
func TestOversizedSubagentLineKeepsParent(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_inner","content":"` + pad(300000) +
		`"}]},"parent_tool_use_id":"toolu_parent","agent_id":"a","session_id":"cli-1"}`
	in := line + "\n"
	r := newStreamLineReaderLimit(strings.NewReader(in), "sid", "test", 1000)
	b, over, err := r.Next()
	require.NoError(t, err)
	require.True(t, over)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "toolu_parent", m["parent_tool_use_id"])
	got := FilterSubagentLines([]string{string(b)}, "toolu_parent")
	assert.Len(t, got, 1)
}

func TestOversizedAssistantLineBecomesSystemNoticeWithParent(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + pad(300000) +
		`"}]},"parent_tool_use_id":"toolu_parent","session_id":"cli-1"}`
	m := replaceLine(t, line)
	assert.Equal(t, "system", m["type"])
	assert.Equal(t, "oversized_output", m["subtype"])
	assert.Equal(t, "toolu_parent", m["parent_tool_use_id"])
	assert.Contains(t, m["message"], "type=assistant")
	meta := m["oversized_output"].(map[string]any)
	assert.Equal(t, "assistant", meta["original_type"])
	assert.Greater(t, meta["bytes"], float64(300000))
}

// Limitation: a user line without a tool_use_id anywhere falls back to the
// plain notice (a tool call, if any, cannot be matched).
func TestOversizedUserWithoutToolUseIDFallsBack(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` + pad(300000) + `"}]},"session_id":"cli-1"}`
	m := replaceLine(t, line)
	assert.Equal(t, "system", m["type"])
	assert.Equal(t, "oversized_output", m["subtype"])
}

func TestOversizedUnknownTypeStillValid(t *testing.T) {
	m := replaceLine(t, `{"weird":"`+pad(300000)+`"}`)
	assert.Equal(t, "system", m["type"])
	assert.Contains(t, m["message"], "type=unknown")
}

// Keys must be found wherever they fall relative to the 64KiB read chunks.
func TestOversizedSniffAcrossChunkBoundaries(t *testing.T) {
	for off := 65000; off < 66200; off += 37 {
		line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + pad(off) +
			`","tool_use_id":"toolu_split"}]},"parent_tool_use_id":"toolu_par","session_id":"cli-1"}`
		m := replaceLine(t, line)
		blk := m["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
		require.Equal(t, "toolu_split", blk["tool_use_id"], "offset %d", off)
		require.Equal(t, "toolu_par", m["parent_tool_use_id"], "offset %d", off)
	}
}

// Normal lines are untouched.
func TestStreamLineReaderPassesNormalLines(t *testing.T) {
	r := NewStreamLineReader(strings.NewReader("{\"a\":1}\r\n\n{\"b\":2}"), "sid", "test")
	var got []string
	for {
		b, over, err := r.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.False(t, over)
		got = append(got, string(b))
	}
	assert.Equal(t, []string{`{"a":1}`, ``, `{"b":2}`}, got)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- file readers: an oversized line followed by a normal one ----

func bigLine(prefix string, n int) string {
	return prefix + strings.Repeat("A", n) + `"}]},"parent_tool_use_id":null,"session_id":"cli-1"}`
}

const bigUserPrefix = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_f","content":"`

func oversizedFixture() string {
	return `{"type":"system","subtype":"init","session_id":"cli-1","model":"sonnet-4"}` + "\n" +
		bigLine(bigUserPrefix, 11*1024*1024) + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"after"}]},"session_id":"cli-2"}` + "\n"
}

func TestReadStreamFileOversizedLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeStreamFixture(t, "ov-1", oversizedFixture())

	m := &Manager{}
	lines, err := m.readStreamFile("ov-1", 0, 0)
	require.NoError(t, err)
	require.Len(t, lines, 3)
	for _, l := range lines {
		assert.True(t, json.Valid([]byte(l)))
	}
	assert.Contains(t, lines[1], "toolu_f")
	assert.Contains(t, lines[1], "出力が大きすぎるため省略しました")
	assert.Contains(t, lines[2], "after")

	// the `after` cursor counts the replacement as one line
	after, err := m.readStreamFileAfter("ov-1", 2, 0)
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Contains(t, after[0], "after")
}

func TestLoadExistingLinesOversizedLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeStreamFixture(t, "ov-2", oversizedFixture())

	sp := &HolderStreamProcess{provider: NewClaudeProvider("claude", "")}
	sp.loadExistingLines("ov-2", 0)
	require.Len(t, sp.lines, 3)
	assert.Contains(t, sp.lines[1], "出力が大きすぎるため省略しました")
	assert.Contains(t, sp.lines[2], "after")
}

func TestReadSessionIDAndModelSkipOversizedLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeStreamFixture(t, "ov-3", oversizedFixture()+`{"type":"system","subtype":"x","session_id":"cli-2"}`+"\n")
	p := NewClaudeProvider("claude", "")

	assert.Equal(t, "sonnet-4", ReadModelFromStream("ov-3", p))
	// the line after the oversized one is still seen
	assert.Equal(t, "cli-2", ReadCLISessionID("ov-3", p, 0))
}

func TestBackgroundTaskRebuildSkipsOversizedLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestBgStore(t)
	streamsDir, err := db.StreamsDir()
	require.NoError(t, err)
	content := `{"type":"system","subtype":"task_started","task_id":"t1","tool_use_id":"toolu_1","description":"d","task_type":"local_bash","session_id":"s"}` + "\n" +
		bigLine(bigUserPrefix, 11*1024*1024) + "\n" +
		`{"type":"system","subtype":"task_started","task_id":"t2","tool_use_id":"toolu_2","description":"d2","task_type":"local_bash","session_id":"s"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(streamsDir, "ov-4.jsonl"), []byte(content), 0o644))

	require.NoError(t, store.RebuildFromJSONL("ov-4", 0))
	tasks, err := store.List("ov-4")
	require.NoError(t, err)
	ids := []string{}
	for _, tk := range tasks {
		ids = append(ids, fmt.Sprint(tk.TaskID))
	}
	assert.ElementsMatch(t, []string{"t1", "t2"}, ids)
}
