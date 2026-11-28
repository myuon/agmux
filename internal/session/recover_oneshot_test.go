package session

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// deadPID returns the PID of a process that has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	proc, err := os.StartProcess("/usr/bin/true", []string{"true"}, &os.ProcAttr{})
	if err != nil {
		t.Skip("cannot start test process:", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	return proc.Pid
}

// A one-shot session (Codex) whose holder exited before a daemon restart must
// not get a new holder at recovery: there is no prompt to give and the CLI
// fails with exit code 1. holder_pid is cleared instead.
func TestRecoverStreamProcesses_OneShotDeadHolderNotSpawned(t *testing.T) {
	for _, tc := range []struct {
		status Status
		want   Status
	}{
		{StatusIdle, StatusIdle},
		{StatusWorking, StatusIdle},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Setenv("TMPDIR", shortTempDir(t))
			t.Setenv("HOME", t.TempDir())

			sqlDB := newTestDB(t)
			t.Cleanup(func() { sqlDB.Close() })
			sqlDB.SetMaxOpenConns(1)
			now := time.Now()
			id := "os1"
			if _, err := sqlDB.Exec(
				`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, "os", "/tmp", "", string(tc.status), "worker", "stream", "codex", "gpt-5", "cli-1", deadPID(t), 1, now, now,
			); err != nil {
				t.Fatalf("insert session: %v", err)
			}
			m := &Manager{
				db:              sqlDB,
				logger:          slog.Default(),
				streamProcesses: make(map[string]*HolderStreamProcess),
				deletingSet:     make(map[string]struct{}),
				codexCommand:    "/nonexistent/codex",
			}

			m.RecoverStreamProcesses()

			m.streamMu.Lock()
			_, spawned := m.streamProcesses[id]
			m.streamMu.Unlock()
			if spawned {
				t.Fatal("a stream process was started for a one-shot session with a dead holder")
			}
			var holderPID int
			var status string
			if err := sqlDB.QueryRow("SELECT holder_pid, status FROM sessions WHERE id = ?", id).Scan(&holderPID, &status); err != nil {
				t.Fatalf("query: %v", err)
			}
			if holderPID != 0 {
				t.Errorf("holder_pid = %d, want 0", holderPID)
			}
			if Status(status) != tc.want {
				t.Errorf("status = %q, want %q", status, tc.want)
			}
		})
	}
}
