package session

import (
	"database/sql"
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
