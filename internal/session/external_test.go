package session

import (
	"testing"
)

func TestFindHolderPIDsFromPS(t *testing.T) {
	const agmuxExe = "/usr/local/bin/agmux"
	psArgsOutput := `  PID ARGS
    1 /sbin/launchd
 1000 /usr/local/bin/agmux holder --session-id abc123 --project-path /home/user/project -- /usr/local/bin/claude
 1001 /usr/local/bin/agmux server
 1002 /usr/local/bin/agmux
 1003 /usr/local/bin/notAgmux holder --session-id xyz
 1004 /usr/local/bin/claude
 2000 /usr/local/bin/agmux holder --session-id def456 --project-path /home/user/other -- /usr/local/bin/codex`

	pids := findHolderPIDsFromPS(psArgsOutput, agmuxExe)

	pidSet := make(map[int]bool)
	for _, pid := range pids {
		pidSet[pid] = true
	}

	if !pidSet[1000] {
		t.Error("expected PID 1000 (agmux holder) to be found")
	}
	if !pidSet[2000] {
		t.Error("expected PID 2000 (agmux holder) to be found")
	}
	if pidSet[1001] {
		t.Error("PID 1001 (agmux server) should not be found")
	}
	if pidSet[1002] {
		t.Error("PID 1002 (bare agmux) should not be found")
	}
	if pidSet[1003] {
		t.Error("PID 1003 (different binary) should not be found")
	}
	if pidSet[1004] {
		t.Error("PID 1004 (claude) should not be found")
	}
}

// TestHolderChildrenExcludedFromExternalDetection verifies that children of holder processes
// that are no longer tracked in the DB (e.g. after a session restart) are still excluded.
// This is a regression test for the bug where stale holders' claude/codex children appeared external.
func TestHolderChildrenExcludedFromExternalDetection(t *testing.T) {
	// Simulate the scenario:
	// - holder PID 35080 is running (not in DB because session was restarted)
	// - holder PID 35080's child claude PID 35081 should NOT appear as external
	//
	// We test the core logic by verifying collectProcessTree correctly includes
	// the grandchildren when given the right psOutput, and that findHolderPIDsFromPS
	// correctly identifies the stale holder.

	const agmuxExe = "/usr/local/bin/agmux"

	// ps -eo pid,ppid,comm output
	psCommOutput := `  PID  PPID COMM
    1     0 launchd
35080     1 /usr/local/bin/agmux
35081 35080 /usr/local/bin/claude`

	// ps -eo pid,args output - holder process identified by args
	psArgsOutput := `  PID ARGS
    1 /sbin/launchd
35080 /usr/local/bin/agmux holder --session-id XYZ --project-path /home/user/project -- /usr/local/bin/claude
35081 /usr/local/bin/claude -p --verbose`

	// Verify findHolderPIDsFromPS finds the stale holder
	holderPIDs := findHolderPIDsFromPS(psArgsOutput, agmuxExe)
	holderPIDSet := make(map[int]bool)
	for _, pid := range holderPIDs {
		holderPIDSet[pid] = true
	}
	if !holderPIDSet[35080] {
		t.Fatal("findHolderPIDsFromPS should find stale holder PID 35080")
	}

	// Verify collectProcessTree correctly finds the claude child
	tree := collectProcessTree(35080, psCommOutput)
	if !tree[35080] {
		t.Error("process tree should include holder PID 35080")
	}
	if !tree[35081] {
		t.Error("process tree should include claude child PID 35081 (regression: this was the bug)")
	}
}

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
