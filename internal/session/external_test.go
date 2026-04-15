package session

import (
	"sort"
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

// extractPIDs is a helper to get sorted PID list from findExternalPIDsFromPS result.
func extractPIDs(result []pidWithProvider) []int {
	pids := make([]int, 0, len(result))
	for _, p := range result {
		pids = append(pids, p.PID)
	}
	sort.Ints(pids)
	return pids
}

func TestFindExternalPIDsFromPS_HolderAlive(t *testing.T) {
	// Scenario: holder is alive, its claude child should be excluded.
	// Process tree:
	//   server (1000) -> (nothing; holders are detached)
	//   holder (200, PPID=1) -> claude (201, PPID=200) [managed]
	//   external-claude (300, PPID=1) [not managed]
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  300     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{200} // holder PID from DB

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	if len(pids) != 1 || pids[0] != 300 {
		t.Errorf("expected [300], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_HolderDead_ChildReparented(t *testing.T) {
	// Scenario: holder died, its child claude got reparented to PID=1.
	// The DB still has the old holder PID (200) but it's not in ps output.
	// The reparented claude (201, now PPID=1) should be treated as external
	// because we can't know it was previously managed.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  201     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{200} // holder PID from DB (dead)

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// PID 201 is reparented to init, former holder 200 is dead.
	// We cannot link 201 to the dead holder 200, so 201 appears external.
	// This is expected behavior: the holder is dead, we can't exclude orphans.
	if len(pids) != 1 || pids[0] != 201 {
		t.Errorf("expected [201] (reparented orphan is external), got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_MultipleHolders(t *testing.T) {
	// Scenario: two managed holders (200, 400), each with a claude child.
	// One additional external claude (600).
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  400     1 /Users/user/go/bin/agmux
  401   400 /Users/user/.local/bin/claude
  600     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{200, 400}

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	if len(pids) != 1 || pids[0] != 600 {
		t.Errorf("expected [600], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_DeepTree(t *testing.T) {
	// Scenario: holder spawns a node process which spawns codex.
	// All should be excluded (BFS traverses multiple levels).
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 node
  202   201 /Users/user/.nodebrew/node/v22/lib/node_modules/codex/codex
  500     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{200}

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// PID 202 (codex via node) should be excluded; only 500 (external claude) should appear
	if len(pids) != 1 || pids[0] != 500 {
		t.Errorf("expected [500], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_FullPathComm(t *testing.T) {
	// Scenario: comm field contains full paths (as seen on macOS).
	// detectProvider should still work via basename extraction.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  300     1 /Users/user/.local/bin/claude
  400     1 /long/path/to/bin/codex`

	myPID := 1000
	managedPIDs := []int{200}

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// PID 201 is managed (child of holder 200)
	// PID 300 (claude) and PID 400 (codex) are external
	if len(pids) != 2 {
		t.Errorf("expected 2 external PIDs, got %v", pids)
	}
	if pids[0] != 300 || pids[1] != 400 {
		t.Errorf("expected [300 400], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_NilManagedPIDs(t *testing.T) {
	// Scenario: no managed PIDs (ManagedHolderPIDs returns nil/empty).
	// All claude/codex processes not in server tree should be external.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/.local/bin/claude
  201   200 node`

	myPID := 1000
	managedPIDs := []int{} // empty: no holder PIDs in DB

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	if len(pids) != 1 || pids[0] != 200 {
		t.Errorf("expected [200], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_ServerChildrenExcluded(t *testing.T) {
	// Scenario: the server itself has claude as a direct child (unusual but possible).
	// That claude should be excluded since it's in the server's tree.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  101  1000 /Users/user/.local/bin/claude
  200     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{}

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// 101 is a direct child of server, so excluded; 200 is external
	if len(pids) != 1 || pids[0] != 200 {
		t.Errorf("expected [200], got %v", pids)
	}
}

func TestFindExternalPIDsFromPS_HolderNotInDB(t *testing.T) {
	// Scenario (core bug): an agmux holder (PPID=1) and its claude child exist,
	// but the holder PID is NOT in managedPIDs (e.g., different server instance,
	// or holder_pid not yet written to DB).
	// The claude child should still be excluded because detectAgmuxHolderPIDs
	// auto-detects all PPID=1 agmux processes as holders.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  999     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{} // holder 200 NOT in DB

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// PID 201 should be excluded (its parent 200 is an agmux holder auto-detected by PPID=1)
	// PID 999 is truly external (PPID=1 but not a child of any agmux holder)
	if len(pids) != 1 || pids[0] != 999 {
		t.Errorf("expected [999] (only truly external claude), got %v (holder-child 201 should be excluded)", pids)
	}
}

func TestFindExternalPIDsFromPS_MultipleAgmuxInstances(t *testing.T) {
	// Scenario: two agmux server instances, each with their own holders.
	// Server 1000 tracks holder 200 in DB. Server 2000 tracks holder 300 in DB
	// (but 2000 and 300 are from a different instance, not in our managedPIDs).
	// Both sets of claude children should be excluded.
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
 2000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  300     1 /Users/user/go/bin/agmux
  301   300 /Users/user/.local/bin/claude
  999     1 /Users/user/.local/bin/claude`

	myPID := 1000
	managedPIDs := []int{200} // only our instance's holder in DB

	result := findExternalPIDsFromPS(myPID, managedPIDs, psOutput)
	pids := extractPIDs(result)

	// Both 201 (from our holder 200) and 301 (from other instance's holder 300) should be excluded.
	// Only 999 (no agmux holder parent) is truly external.
	if len(pids) != 1 || pids[0] != 999 {
		t.Errorf("expected [999], got %v", pids)
	}
}

func TestDetectAgmuxHolderPIDs(t *testing.T) {
	psOutput := `  PID  PPID COMM
    1     0 /sbin/launchd
 1000     1 /Users/user/go/bin/agmux-server
  200     1 /Users/user/go/bin/agmux
  201   200 /Users/user/.local/bin/claude
  300     1 agmux
  400   100 /Users/user/go/bin/agmux
  500     1 /Users/user/.local/bin/claude`

	holders := detectAgmuxHolderPIDs(psOutput)
	sort.Ints(holders)

	// 200: PPID=1, comm basename "agmux" -> holder
	// 300: PPID=1, comm "agmux" -> holder
	// 400: PPID=100 (not 1) -> not a holder (it's a server or child)
	// 1000: PPID=1, comm "agmux-server" (not "agmux") -> not a holder
	expected := []int{200, 300}
	if len(holders) != len(expected) {
		t.Errorf("expected %v, got %v", expected, holders)
		return
	}
	for i, h := range holders {
		if h != expected[i] {
			t.Errorf("expected %v, got %v", expected, holders)
			return
		}
	}
}
