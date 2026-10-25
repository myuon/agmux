package session

import (
	"reflect"
	"testing"
)

func TestFilterSubagentLines(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"agent1","name":"Agent","input":{"prompt":"p"}}]},"parent_tool_use_id":null}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"sub prompt"}]},"parent_tool_use_id":"agent1"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bash1","name":"Bash","input":{"command":"ls"}}]},"parent_tool_use_id":"agent1"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bash1","content":"ok"}]},"parent_tool_use_id":"agent1"}`,
		// nested subagent spawned inside agent1
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"task2","name":"Task","input":{"prompt":"nested"}}]},"parent_tool_use_id":"agent1"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"read3","name":"Read","input":{}}]},"parent_tool_use_id":"task2"}`,
		// another, unrelated subagent
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"x","name":"Bash","input":{}}]},"parent_tool_use_id":"other"}`,
		`not json`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"agent1","content":"done"}]},"parent_tool_use_id":null}`,
	}

	got := FilterSubagentLines(lines, "agent1")
	want := []string{lines[2], lines[3], lines[4], lines[5], lines[6]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent1: got %v\nwant %v", got, want)
	}

	got = FilterSubagentLines(lines, "task2")
	want = []string{lines[6]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task2: got %v\nwant %v", got, want)
	}

	if got := FilterSubagentLines(lines, "missing"); len(got) != 0 {
		t.Fatalf("missing: expected empty, got %v", got)
	}
	if got := FilterSubagentLines(lines, ""); len(got) != 0 {
		t.Fatalf("empty id: expected empty, got %v", got)
	}
}
