package session

import (
	"os/exec"
	"testing"
	"time"
)

// waitHolderObserved registers an observer and returns a channel that is
// closed once the exit of pid has been reported to all observers registered
// before it (including the Manager's holder_pid clear).
func waitHolderObserved(pid int) <-chan struct{} {
	ch := make(chan struct{})
	addHolderExitObserver(func(_ string, p int) {
		if p == pid {
			close(ch)
		}
	})
	return ch
}

func insertHolderSession(t *testing.T, m *Manager, id string, pid int) {
	t.Helper()
	now := time.Now()
	if _, err := m.db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", string(StatusIdle), "worker", "stream", "claude", "", pid, now, now,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func dbHolderPIDAndStatus(t *testing.T, m *Manager, id string) (int, string) {
	t.Helper()
	var pid int
	var status string
	if err := m.db.QueryRow("SELECT holder_pid, status FROM sessions WHERE id = ?", id).Scan(&pid, &status); err != nil {
		t.Fatalf("query session: %v", err)
	}
	return pid, status
}

// After a tracked holder is reaped, its PID may be reused by an unrelated
// process, so the session's holder_pid must be cleared (without touching the
// status), and the PID must be treated as dead until the session spawns a new
// holder.
func TestReapedHolder_ClearsHolderPID(t *testing.T) {
	m := newTestManagerFull(t)

	child := exec.Command("sh", "-c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := child.Process.Pid
	insertHolderSession(t, m, "reap1", pid)
	observed := waitHolderObserved(pid)
	trackHolderExit("reap1", child.Process)

	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("holder exit was not observed")
	}

	gotPID, status := dbHolderPIDAndStatus(t, m, "reap1")
	if gotPID != 0 {
		t.Fatalf("holder_pid = %d, want 0", gotPID)
	}
	if status != string(StatusIdle) {
		t.Fatalf("status = %q, want unchanged %q", status, StatusIdle)
	}
	if !holderReaped(pid) || IsHolderAlive(pid) {
		t.Fatal("reaped holder PID must be treated as dead")
	}

	// Spawning a new holder for the same session drops the old exit record.
	next := exec.Command("sh", "-c", "exit 0")
	if err := next.Start(); err != nil {
		t.Fatalf("start next: %v", err)
	}
	trackHolderExit("reap1", next.Process)
	if next.Process.Pid != pid && holderReaped(pid) {
		t.Fatal("old exit record should be dropped once the session spawns a new holder")
	}
}

// An older holder's exit must not clear the PID of a newer holder that has
// already replaced it in the DB.
func TestReapedHolder_DoesNotClearNewerHolderPID(t *testing.T) {
	m := newTestManagerFull(t)

	older := exec.Command("sh", "-c", "read x")
	stdin, err := older.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := older.Start(); err != nil {
		t.Fatalf("start older: %v", err)
	}
	trackHolderExit("reap2", older.Process)

	newer := exec.Command("sleep", "60")
	if err := newer.Start(); err != nil {
		t.Fatalf("start newer: %v", err)
	}
	t.Cleanup(func() { newer.Process.Kill() })
	trackHolderExit("reap2", newer.Process)
	insertHolderSession(t, m, "reap2", newer.Process.Pid)

	observed := waitHolderObserved(older.Process.Pid)
	stdin.Close()
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("older holder exit was not observed")
	}

	if gotPID, _ := dbHolderPIDAndStatus(t, m, "reap2"); gotPID != newer.Process.Pid {
		t.Fatalf("holder_pid = %d, want newer holder %d", gotPID, newer.Process.Pid)
	}
	if !IsHolderAlive(newer.Process.Pid) {
		t.Fatal("newer holder should still be alive")
	}
}
