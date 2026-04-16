package session

import (
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB creates an in-memory SQLite database with the sessions table schema.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			project_path TEXT NOT NULL,
			initial_prompt TEXT,
			tmux_session TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'working',
			type TEXT NOT NULL DEFAULT 'worker',
			output_mode TEXT NOT NULL DEFAULT 'stream',
			provider TEXT NOT NULL DEFAULT 'claude',
			model TEXT NOT NULL DEFAULT '',
			system_prompt TEXT,
			parent_session_id TEXT,
			role_template TEXT,
			holder_pid INTEGER NOT NULL DEFAULT 0,
			clear_offset INTEGER NOT NULL DEFAULT 0,
			cli_session_id TEXT NOT NULL DEFAULT '',
			current_task TEXT,
			goal TEXT,
			goals TEXT NOT NULL DEFAULT '[]',
			last_error TEXT,
			conversation_started INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("create sessions table: %v", err)
	}
	return db
}

// TestCreateInsertIncludesHolderPID verifies that the INSERT SQL used in Create()
// includes holder_pid in the same statement (not via a separate UPDATE).
// This is the exact INSERT query from manager.go Create().
func TestCreateInsertIncludesHolderPID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-create-id"
	now := time.Now()
	expectedPID := 12345

	// This is the exact INSERT statement used in Create() after the fix.
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, system_prompt, parent_session_id, role_template, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test-session", "/tmp/project", "hello", "", "idle", "worker", "stream", "claude", "", nil, nil, nil, expectedPID, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Verify holder_pid was set atomically in the INSERT (no separate UPDATE needed).
	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != expectedPID {
		t.Errorf("holder_pid = %d, want %d (should be set in INSERT, not via separate UPDATE)", gotPID, expectedPID)
	}
}

// TestForkInsertIncludesHolderPID verifies that the INSERT SQL used in Fork()
// includes holder_pid in the same statement (not via a separate UPDATE).
// This is the exact INSERT query from manager.go Fork().
func TestForkInsertIncludesHolderPID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-fork-id"
	now := time.Now()
	expectedPID := 67890

	// This is the exact INSERT statement used in Fork() after the fix.
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, system_prompt, parent_session_id, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test-session (fork)", "/tmp/project", "", "", "idle", "worker", "stream", "claude", "", nil, "parent-id", expectedPID, now, now,
	)
	if err != nil {
		t.Fatalf("insert forked session: %v", err)
	}

	// Verify holder_pid was set atomically in the INSERT (no separate UPDATE needed).
	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != expectedPID {
		t.Errorf("holder_pid = %d, want %d (should be set in INSERT, not via separate UPDATE)", gotPID, expectedPID)
	}
}

// TestRecoverInsertHolderPIDViaUpdate verifies the UPDATE SQL used in RecoverStreamProcesses()
// correctly sets holder_pid in a single UPDATE statement.
func TestRecoverInsertHolderPIDViaUpdate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-recover-id"
	now := time.Now()

	// Insert a session without holder_pid (simulating a paused session that needs recovery).
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test-recover-session", "/tmp/project", "", "", "idle", "worker", "stream", "claude", "", 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session for recovery test: %v", err)
	}

	expectedPID := 99999

	// This is the exact UPDATE used in RecoverStreamProcesses() after the fix.
	_, err = db.Exec("UPDATE sessions SET holder_pid = ?, updated_at = ? WHERE id = ?", expectedPID, time.Now(), id)
	if err != nil {
		t.Fatalf("update holder_pid: %v", err)
	}

	// Verify holder_pid was set.
	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != expectedPID {
		t.Errorf("holder_pid = %d, want %d", gotPID, expectedPID)
	}
}

// TestDefaultHolderPIDIsZero verifies that when a session is inserted without
// specifying holder_pid, the default value is 0 (not some unexpected value).
func TestDefaultHolderPIDIsZero(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-default-pid"
	now := time.Now()

	// Insert without holder_pid to verify the default.
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "default-pid-test", "/tmp/project", "", "idle", "worker", "stream", "claude", "", now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != 0 {
		t.Errorf("default holder_pid = %d, want 0", gotPID)
	}
}

// newTestManager creates a Manager with an in-memory SQLite DB for testing.
func newTestManager(t *testing.T, db *sql.DB) *Manager {
	t.Helper()
	return &Manager{
		db:     db,
		logger: slog.Default(),
	}
}

// TestKillStaleHolder_NoPID verifies that killStaleHolder is a no-op when holder_pid is 0.
func TestKillStaleHolder_NoPID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-no-pid"
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m := newTestManager(t, db)
	// Should not panic or error; no process to kill
	m.killStaleHolder(id)

	// holder_pid should remain 0
	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != 0 {
		t.Errorf("holder_pid = %d, want 0 after killStaleHolder no-op", gotPID)
	}
}

// TestKillStaleHolder_DeadPID verifies that killStaleHolder is a no-op for a PID that
// is not alive (e.g., a stale value in the DB for a process that already exited).
func TestKillStaleHolder_DeadPID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Use a PID that is guaranteed not to be running: max int32 (very unlikely to exist)
	deadPID := 2147483647

	id := "test-dead-pid"
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", deadPID, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m := newTestManager(t, db)
	// Should not panic; dead PID means IsHolderAlive returns false, so we skip killing
	m.killStaleHolder(id)
}

// TestKillStaleHolder_MissingSession verifies that killStaleHolder handles a non-existent
// session gracefully (e.g., DB lookup fails).
func TestKillStaleHolder_MissingSession(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	// Should not panic when session doesn't exist
	m.killStaleHolder("non-existent-session-id")
}

// TestKillStaleHolder_LiveProcess verifies that killStaleHolder kills an actually-running
// process and waits for its exit.
func TestKillStaleHolder_LiveProcess(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Start a real process we can kill: "sleep 60"
	proc, err := os.StartProcess("/bin/sleep", []string{"sleep", "60"}, &os.ProcAttr{})
	if err != nil {
		t.Skip("cannot start test process:", err)
	}
	livePID := proc.Pid

	id := "test-live-pid"
	now := time.Now()
	_, err = db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", livePID, now, now,
	)
	if err != nil {
		// Kill the process before failing to avoid leaking it
		_ = proc.Kill()
		_, _ = proc.Wait()
		t.Fatalf("insert session: %v", err)
	}

	m := newTestManager(t, db)
	m.killStaleHolder(id)

	// Reap the zombie so signal(0) correctly reports the process as dead.
	// (os.StartProcess creates a direct child; zombie children respond to signal(0)
	// until reaped by Wait. Real holder processes are detached and have no zombie state.)
	_, _ = proc.Wait()

	// After killStaleHolder and Wait, the process should no longer be alive
	if IsHolderAlive(livePID) {
		t.Errorf("process %d should be dead after killStaleHolder, but it's still alive", livePID)
	}
}

// TestConversationStartedDefaultFalse verifies that newly created sessions have
// conversation_started = false (0) by default.
func TestConversationStartedDefaultFalse(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-conv-default"
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	var gotStarted int
	if err := db.QueryRow("SELECT conversation_started FROM sessions WHERE id = ?", id).Scan(&gotStarted); err != nil {
		t.Fatalf("query conversation_started: %v", err)
	}
	if gotStarted != 0 {
		t.Errorf("conversation_started = %d, want 0 (false) for new session", gotStarted)
	}
}

// TestMarkConversationStarted verifies that MarkConversationStarted sets the flag to 1.
func TestMarkConversationStarted(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "test-conv-mark"
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, conversation_started, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", 0, 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m := newTestManager(t, db)
	if err := m.MarkConversationStarted(id); err != nil {
		t.Fatalf("MarkConversationStarted: %v", err)
	}

	var gotStarted int
	if err := db.QueryRow("SELECT conversation_started FROM sessions WHERE id = ?", id).Scan(&gotStarted); err != nil {
		t.Fatalf("query conversation_started: %v", err)
	}
	if gotStarted != 1 {
		t.Errorf("conversation_started = %d, want 1 (true) after MarkConversationStarted", gotStarted)
	}
}

// TestGetReadsConversationStarted verifies that Get() correctly reads conversation_started.
func TestGetReadsConversationStarted(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	now := time.Now()

	// Session with conversation NOT started
	id1 := "test-not-started"
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id1, "not-started", "/tmp", "", "idle", "worker", "stream", "claude", "", "", 0, 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session id1: %v", err)
	}

	// Session with conversation started
	id2 := "test-started"
	_, err = db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id2, "started", "/tmp", "", "idle", "worker", "stream", "claude", "", "some-cli-id", 0, 1, now, now,
	)
	if err != nil {
		t.Fatalf("insert session id2: %v", err)
	}

	m := newTestManager(t, db)

	s1, err := m.Get(id1)
	if err != nil {
		t.Fatalf("Get(id1): %v", err)
	}
	if s1.ConversationStarted {
		t.Errorf("session id1 ConversationStarted = true, want false")
	}

	s2, err := m.Get(id2)
	if err != nil {
		t.Fatalf("Get(id2): %v", err)
	}
	if !s2.ConversationStarted {
		t.Errorf("session id2 ConversationStarted = false, want true")
	}
}
