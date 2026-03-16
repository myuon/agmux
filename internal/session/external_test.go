package session

import (
	"testing"
)

func TestCollectProcessTree(t *testing.T) {
	// Simulated ps output
	psOutput := `  PID  PPID COMM
    1     0 init
  100     1 agmux
  200   100 claude
  300   100 bash
  400   300 claude
  500     1 claude
  600    50 claude
  700     1 codex
  800   100 codex`

	tree := collectProcessTree(100, psOutput)

	// Should include root and all descendants
	if !tree[100] {
		t.Error("expected root PID 100 to be in tree")
	}
	if !tree[200] {
		t.Error("expected child PID 200 to be in tree")
	}
	if !tree[300] {
		t.Error("expected child PID 300 to be in tree")
	}
	if !tree[400] {
		t.Error("expected grandchild PID 400 to be in tree")
	}
	if !tree[800] {
		t.Error("expected child PID 800 (codex) to be in tree")
	}

	// Should NOT include external processes
	if tree[500] {
		t.Error("PID 500 should not be in agmux tree")
	}
	if tree[600] {
		t.Error("PID 600 should not be in agmux tree")
	}
	if tree[700] {
		t.Error("PID 700 should not be in agmux tree")
	}
}

func TestIsClaudeProcess(t *testing.T) {
	tests := []struct {
		comm string
		want bool
	}{
		{"claude", true},
		{"/usr/local/bin/claude", true},
		{"/opt/homebrew/bin/claude", true},
		{"node", false},
		{"claudeX", false},
		{"bash", false},
		{"", false},
		{"codex", false},
	}

	for _, tt := range tests {
		got := isClaudeProcess(tt.comm)
		if got != tt.want {
			t.Errorf("isClaudeProcess(%q) = %v, want %v", tt.comm, got, tt.want)
		}
	}
}

func TestIsCodexProcess(t *testing.T) {
	tests := []struct {
		comm string
		want bool
	}{
		{"codex", true},
		{"/usr/local/bin/codex", true},
		{"/opt/homebrew/bin/codex", true},
		{"node", false},
		{"codexX", false},
		{"claude", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isCodexProcess(tt.comm)
		if got != tt.want {
			t.Errorf("isCodexProcess(%q) = %v, want %v", tt.comm, got, tt.want)
		}
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		comm string
		want ProviderName
	}{
		{"claude", ProviderClaude},
		{"/usr/local/bin/claude", ProviderClaude},
		{"codex", ProviderCodex},
		{"/usr/local/bin/codex", ProviderCodex},
		{"node", ""},
		{"bash", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := detectProvider(tt.comm)
		if got != tt.want {
			t.Errorf("detectProvider(%q) = %q, want %q", tt.comm, got, tt.want)
		}
	}
}

func TestProjectNameFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/user/projects/myapp", "myapp"},
		{"/Users/user/projects/myapp/", "myapp"},
		{"myapp", "myapp"},
		{"", ""},
	}

	for _, tt := range tests {
		got := projectNameFromPath(tt.path)
		if got != tt.want {
			t.Errorf("projectNameFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
