package mcp

import (
	"encoding/json"
	"testing"

	"github.com/myuon/agmux/internal/experimental"
)

func listToolNames(t *testing.T) map[string]bool {
	t.Helper()
	s := NewServerForHTTP("sess", "http://127.0.0.1:0")
	res, rpcErr := s.handleMethod("tools/list", nil)
	if rpcErr != nil {
		t.Fatalf("tools/list: %v", rpcErr)
	}
	b, _ := json.Marshal(res)
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range out.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestLoopToolsGatedByLoopsFeature(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loopTools := []string{"loop_task_add", "loop_task_next", "loop_task_done"}

	names := listToolNames(t)
	s := NewServerForHTTP("sess", "http://127.0.0.1:0")
	for _, n := range loopTools {
		if names[n] {
			t.Errorf("%s must be hidden while loops is off", n)
		}
		if _, err := s.callTool(n, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s call must be rejected while loops is off", n)
		}
	}
	if !names["create_goal"] {
		t.Error("ungated tools must stay visible")
	}

	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}
	names = listToolNames(t)
	for _, n := range loopTools {
		if !names[n] {
			t.Errorf("%s must be visible while loops is on", n)
		}
	}
}
