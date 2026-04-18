package session

import (
	"log/slog"
	"testing"
	"time"
)

// newTestManagerFull creates a Manager (with all fields initialised) backed by
// an in-memory SQLite DB.  Unlike the newTestManager helper in manager_holderPID_test.go
// which only sets db/logger, this version uses NewManager so that maps like
// ephemeralCancels are properly initialised.
//
// MaxOpenConns is set to 1 so that all queries share a single connection and
// therefore see the same :memory: SQLite database instance.
func newTestManagerFull(t *testing.T) *Manager {
	t.Helper()
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return NewManager(db, "claude", "default", 0, slog.Default(), "")
}

// insertEphemeralSession inserts a minimal ephemeral session row directly into
// the DB so that recoverEphemeralTimeouts / startEphemeralTimeout can operate
// on it without requiring a running holder process.
func insertEphemeralSession(t *testing.T, m *Manager, id string, timeoutSeconds int, createdAt time.Time) {
	t.Helper()
	_, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, initial_prompt, tmux_session, status, type, output_mode, provider, model, holder_pid, ephemeral_timeout_seconds, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "", string(StatusIdle), string(TypeEphemeral), "stream", "claude", "", 0,
		timeoutSeconds, createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("insertEphemeralSession: %v", err)
	}
}

// TestRecoverEphemeralTimeouts_AlreadyExpired verifies that recoverEphemeralTimeouts
// immediately archives a session whose deadline has already passed.
func TestRecoverEphemeralTimeouts_AlreadyExpired(t *testing.T) {
	m := newTestManagerFull(t)

	id := "expire-now"
	// created 10 minutes ago with a 5-minute timeout → already expired
	createdAt := time.Now().Add(-10 * time.Minute)
	insertEphemeralSession(t, m, id, 300 /* 5 min */, createdAt)

	m.recoverEphemeralTimeouts()

	var status string
	var holderPID int
	if err := m.db.QueryRow("SELECT status, holder_pid FROM sessions WHERE id = ?", id).Scan(&status, &holderPID); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != string(StatusArchived) {
		t.Errorf("status = %q, want %q", status, StatusArchived)
	}
	if holderPID != 0 {
		t.Errorf("holder_pid = %d, want 0", holderPID)
	}
}

// TestRecoverEphemeralTimeouts_NotYetExpired verifies that recoverEphemeralTimeouts
// launches a goroutine (and doesn't immediately archive) for a session that still
// has time remaining, and that the goroutine archives it after the remaining time elapses.
func TestRecoverEphemeralTimeouts_NotYetExpired(t *testing.T) {
	m := newTestManagerFull(t)

	id := "expire-soon"
	// created 1 second ago with a 2-second timeout → ~1 second remaining
	createdAt := time.Now().Add(-1 * time.Second)
	insertEphemeralSession(t, m, id, 2 /* 2 sec */, createdAt)

	m.recoverEphemeralTimeouts()

	// Immediately after recovery the session should still be idle
	var status string
	if err := m.db.QueryRow("SELECT status FROM sessions WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != string(StatusIdle) {
		t.Errorf("status immediately after recover = %q, want %q", status, StatusIdle)
	}

	// Wait for the goroutine to fire (remaining ≈ 1s; allow 3s for CI slack)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if err := m.db.QueryRow("SELECT status FROM sessions WHERE id = ?", id).Scan(&status); err != nil {
			t.Fatalf("query session: %v", err)
		}
		if status == string(StatusArchived) {
			break
		}
	}
	if status != string(StatusArchived) {
		t.Errorf("status after timeout = %q, want %q", status, StatusArchived)
	}
}

// TestCancelEphemeralTimeout verifies that cancelling an ephemeral timeout goroutine
// prevents it from archiving the session.
func TestCancelEphemeralTimeout(t *testing.T) {
	m := newTestManagerFull(t)

	id := "cancel-me"
	createdAt := time.Now()
	insertEphemeralSession(t, m, id, 3600 /* 1h */, createdAt)

	// Start a goroutine that would fire in 200ms (short for test speed)
	m.startEphemeralTimeout(id, "", 200*time.Millisecond)

	// Cancel it immediately
	m.cancelEphemeralTimeout(id)

	// Wait slightly longer than 200ms to give the goroutine time to (not) run
	time.Sleep(400 * time.Millisecond)

	var status string
	if err := m.db.QueryRow("SELECT status FROM sessions WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatalf("query session: %v", err)
	}
	// Session should remain idle because the goroutine was cancelled
	if status != string(StatusIdle) {
		t.Errorf("status = %q after cancel, want %q (goroutine should have been cancelled)", status, StatusIdle)
	}
}
