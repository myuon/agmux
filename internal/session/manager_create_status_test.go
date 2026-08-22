package session

import (
	"log/slog"
	"testing"
	"time"
)

// Regression tests for issue #708: a session created via POST /api/sessions
// with an initial prompt stayed "idle" for the entire first turn, so UI that
// keys off "working" (e.g. the running-tool spinner) never engaged. The send
// endpoint, by contrast, promotes the session to "working" right after
// SendKeysWithImages.
//
// Manager.Create cannot be exercised end-to-end here because a non-empty
// prompt makes it spawn a real holder process, so the status decision is
// covered through the pure helper it delegates to (same approach as
// buildOneShotResumeOpts in manager_lazy_spawn_test.go).

func TestInitialCreateStatus(t *testing.T) {
	cases := []struct {
		name          string
		holderSpawned bool
		prompt        string
		want          Status
	}{
		{
			name:          "prompt handed to a spawned holder starts working",
			holderSpawned: true,
			prompt:        "do the thing",
			want:          StatusWorking,
		},
		{
			name:          "no prompt starts idle even though a holder is running",
			holderSpawned: true,
			prompt:        "",
			want:          StatusIdle,
		},
		{
			name:          "deferred one-shot spawn starts idle (see #643)",
			holderSpawned: false,
			prompt:        "do the thing",
			want:          StatusIdle,
		},
		{
			name:          "no holder and no prompt starts idle",
			holderSpawned: false,
			prompt:        "",
			want:          StatusIdle,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := initialCreateStatus(tc.holderSpawned, tc.prompt); got != tc.want {
				t.Errorf("initialCreateStatus(%v, %q) = %q, want %q",
					tc.holderSpawned, tc.prompt, got, tc.want)
			}
		})
	}
}

// TestCreate_NoPrompt_StaysIdle verifies that the #708 fix did not regress the
// prompt-less create path: with nothing sent to the CLI there is no turn in
// flight, so the row must still be INSERTed as idle (otherwise the session
// would be stuck at "working" forever — no turn-complete event is coming).
func TestCreate_NoPrompt_StaysIdle(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	m := &Manager{
		db:              db,
		logger:          slog.Default(),
		cursorCommand:   "agent",
		codexCommand:    "codex",
		claudeCommand:   "claude",
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
		systemPrompt:    "test",
	}

	// Codex + empty prompt takes the lazy-spawn branch, so no holder (and no
	// external CLI) is needed for this test.
	sess, err := m.Create("no-prompt", "/tmp", "", false, CreateOpts{Provider: ProviderCodex})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Status != StatusIdle {
		t.Errorf("returned session status = %q, want %q", sess.Status, StatusIdle)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM sessions WHERE id = ?", sess.ID).Scan(&status); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if status != string(StatusIdle) {
		t.Errorf("persisted status = %q, want %q", status, string(StatusIdle))
	}
}

// TestForkInsertUsesWorkingStatus verifies that the exact INSERT used by Fork()
// persists status = working when the row is built with initialCreateStatus.
// Fork hands initialPrompt to the CLI (stdin for Claude, positional arg for
// one-shot providers) immediately before this INSERT, so the first turn is
// already running — the row must not say idle. Mirrors the SQL-shape style of
// TestForkInsertIncludesHolderPID in manager_holderPID_test.go, since a real
// Fork spawns a holder process.
func TestForkInsertUsesWorkingStatus(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-fork-status"
	now := time.Now()
	initialPrompt := "continue from here"

	// Fork always spawns a holder (it returns an error otherwise) and always
	// has a non-empty prompt, hence holderSpawned = true.
	status := initialCreateStatus(true, initialPrompt)

	// This is the exact INSERT statement used in Fork().
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, system_prompt, parent_session_id, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test-session (fork)", "/tmp/project", initialPrompt, "", string(status), "worker", "stream", "claude", "", nil, "parent-id", 12345, now, now,
	)
	if err != nil {
		t.Fatalf("insert forked session: %v", err)
	}

	var got string
	if err := db.QueryRow("SELECT status FROM sessions WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if got != string(StatusWorking) {
		t.Errorf("persisted status = %q, want %q (fork's first turn is already running)", got, string(StatusWorking))
	}
}

// TestFork_EmptyPrompt_Rejected pins the precondition that makes
// initialCreateStatus always return working inside Fork: an empty initialPrompt
// never reaches the INSERT, so there is no "forked but nothing running" row
// that could get stuck in working.
func TestFork_EmptyPrompt_Rejected(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	m := &Manager{
		db:              db,
		logger:          slog.Default(),
		claudeCommand:   "claude",
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
		systemPrompt:    "test",
	}

	for _, prompt := range []string{"", "   "} {
		if _, err := m.Fork("some-session", true, prompt); err == nil {
			t.Errorf("Fork with prompt %q should fail, got nil error", prompt)
		}
	}
}
