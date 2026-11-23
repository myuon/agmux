package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSubagentFixture(t *testing.T, dir, agentID, toolUseID string, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	meta := `{"agentType":"general-purpose","toolUseId":"` + toolUseID + `","requestShape":"background"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".meta.json"), []byte(meta), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-"+agentID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
}

func decodeLines(t *testing.T, lines []string) []map[string]interface{} {
	t.Helper()
	out := make([]map[string]interface{}, 0, len(lines))
	for _, l := range lines {
		var m map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(l), &m))
		out = append(out, m)
	}
	return out
}

func TestClaudeSubagentsDir(t *testing.T) {
	got := ClaudeSubagentsDir("/home/u", "/Users/u/.agmux/controller", "sid-1")
	assert.Equal(t, filepath.Join("/home/u", ".claude", "projects", "-Users-u--agmux-controller", "sid-1", "subagents"), got)
}

func TestReadSubagentTranscriptLines_ConvertsUserAndAssistant(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subagents")
	writeSubagentFixture(t, dir, "a1", "toolu_parent",
		`{"type":"user","isSidechain":true,"agentId":"a1","uuid":"u1","sessionId":"s","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"do it"}}`,
		`{"type":"attachment","timestamp":"2026-01-01T00:00:00.500Z","attachment":{}}`,
		`{"type":"assistant","uuid":"u2","sessionId":"s","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","uuid":"u3","sessionId":"s","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"ok"}]}}`,
		`not json`,
	)
	// Unrelated subagent must not be included.
	writeSubagentFixture(t, dir, "a2", "toolu_other",
		`{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"other"}}`,
	)

	got := decodeLines(t, ReadSubagentTranscriptLines(dir, "toolu_parent"))
	require.Len(t, got, 3)
	assert.Equal(t, []interface{}{"user", "assistant", "user"}, []interface{}{got[0]["type"], got[1]["type"], got[2]["type"]})
	for _, l := range got {
		assert.Equal(t, "toolu_parent", l["parent_tool_use_id"])
		assert.Equal(t, "s", l["session_id"])
	}
	msg := got[1]["message"].(map[string]interface{})
	block := msg["content"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "tool_use", block["type"])
	assert.Equal(t, "Bash", block["name"])
}

func TestReadSubagentTranscriptLines_IncludesNestedSubagents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subagents")
	writeSubagentFixture(t, dir, "parent", "toolu_parent",
		`{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"start"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_child","name":"Agent","input":{"prompt":"sub"}}]}}`,
		`{"type":"user","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_child","content":"done"}]}}`,
	)
	writeSubagentFixture(t, dir, "child", "toolu_child",
		`{"type":"user","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"user","content":"sub"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	)

	got := decodeLines(t, ReadSubagentTranscriptLines(dir, "toolu_parent"))
	require.Len(t, got, 5)
	parents := []interface{}{}
	for _, l := range got {
		parents = append(parents, l["parent_tool_use_id"])
	}
	assert.Equal(t, []interface{}{"toolu_parent", "toolu_parent", "toolu_child", "toolu_child", "toolu_parent"}, parents)

	// The nested transcript can also be read on its own.
	assert.Len(t, ReadSubagentTranscriptLines(dir, "toolu_child"), 2)
}

func TestReadSubagentTranscriptLines_MissingReturnsEmpty(t *testing.T) {
	base := t.TempDir()
	assert.Empty(t, ReadSubagentTranscriptLines(filepath.Join(base, "nope"), "toolu_x"))
	assert.NotNil(t, ReadSubagentTranscriptLines(filepath.Join(base, "nope"), "toolu_x"))

	dir := filepath.Join(base, "subagents")
	writeSubagentFixture(t, dir, "a1", "toolu_parent",
		`{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"x"}}`)
	assert.Empty(t, ReadSubagentTranscriptLines(dir, "toolu_unknown"))
	assert.Empty(t, ReadSubagentTranscriptLines(dir, ""))

	// Meta without its transcript file.
	require.NoError(t, os.Remove(filepath.Join(dir, "agent-a1.jsonl")))
	assert.Empty(t, ReadSubagentTranscriptLines(dir, "toolu_parent"))
}
