package mcp

import (
	"testing"

	"github.com/myuon/agmux/internal/experimental"
)

func TestFilterExperimentalTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origF, origT := experimental.Features, experimentalTools
	experimental.Features = []experimental.Feature{{Name: "feat_x"}}
	experimentalTools = map[string]string{"tool_x": "feat_x"}
	t.Cleanup(func() { experimental.Features, experimentalTools = origF, origT })

	tools := []interface{}{
		map[string]interface{}{"name": "create_goal"},
		map[string]interface{}{"name": "tool_x"},
	}
	if got := filterExperimentalTools(tools); len(got) != 1 {
		t.Fatalf("expected gated tool hidden, got %v", got)
	}
	s := NewServerForHTTP("sess", "http://127.0.0.1:0")
	if _, err := s.callTool("tool_x", nil); err == nil {
		t.Fatal("expected gated tool call to be rejected")
	}

	if err := experimental.Set("feat_x", true); err != nil {
		t.Fatal(err)
	}
	if got := filterExperimentalTools(tools); len(got) != 2 {
		t.Fatalf("expected gated tool visible, got %v", got)
	}
}
