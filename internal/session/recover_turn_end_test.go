package session

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
)

const (
	recInit      = `{"type":"system","subtype":"init","session_id":"cli-1","model":"opus"}`
	recUser      = `{"type":"user","message":{"role":"user","content":"do it"}}`
	recAssistant = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"session_id":"cli-1"}`
	recResult    = `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"cli-1","uuid":"r-1"}`
	recErrResult = `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"cli-1"}`
	recDelta     = `{"type":"stream_event","event":{"type":"message_stop"},"session_id":"cli-1"}`
	recTaskStart = `{"type":"system","subtype":"task_started","task_id":"t1"}`
	recTaskDone  = `{"type":"system","subtype":"task_notification","task_id":"t1"}`
)

func writeRecoverStream(t *testing.T, sessionID string, lines ...string) {
	t.Helper()
	dir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExistingLines_ReplayedTurnEnd(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  TurnEnd
	}{
		{"empty", nil, TurnEndNone},
		{"ends with result", []string{recInit, recUser, recAssistant, recDelta, recResult}, TurnEndComplete},
		{"result then system noise", []string{recUser, recAssistant, recResult, recTaskStart, recTaskDone}, TurnEndComplete},
		{"new user input after result", []string{recUser, recAssistant, recResult, recUser}, TurnEndNone},
		{"assistant after result", []string{recUser, recResult, recUser, recAssistant}, TurnEndNone},
		{"turn in progress", []string{recInit, recUser, recAssistant}, TurnEndNone},
		{"result while background task runs", []string{recUser, recTaskStart, recAssistant, recResult}, TurnEndNone},
		{"result after background task finished", []string{recUser, recTaskStart, recTaskDone, recAssistant, recResult}, TurnEndComplete},
		{"task left over from an earlier turn", []string{recUser, recTaskStart, recResult, recUser, recAssistant, recResult}, TurnEndComplete},
		{"task finished after a result, no new result yet", []string{recUser, recTaskStart, recResult, recTaskDone}, TurnEndNone},
		{"task id reused", []string{recUser, recTaskStart, recTaskDone, recResult, recUser, recTaskStart, recResult}, TurnEndNone},
		{"task killed", []string{recUser, recTaskStart, `{"type":"system","subtype":"task_updated","task_id":"t1","patch":{"status":"killed"}}`, recResult}, TurnEndComplete},
		{"error result", []string{recUser, recAssistant, recErrResult}, TurnEndFailed},
		{"codex turn.completed", []string{`{"type":"turn.started"}`, `{"type":"turn.completed","usage":{}}`}, TurnEndComplete},
		{"codex turn.started after completed", []string{`{"type":"turn.completed"}`, `{"type":"turn.started"}`}, TurnEndNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeRecoverStream(t, "s1", tc.lines...)
			sp := &HolderStreamProcess{provider: NewClaudeProvider("", "")}
			sp.loadExistingLines("s1", 0)
			if got := sp.ReplayedTurnEnd(); got != tc.want {
				t.Fatalf("ReplayedTurnEnd = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadExistingLines_ReplayedTurnEndRespectsClearOffset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeRecoverStream(t, "s1", recUser, recResult)
	// Clear() recorded an offset at the end of the file: nothing after it.
	offset := int64(len(recUser) + 1 + len(recResult) + 1)
	sp := &HolderStreamProcess{provider: NewClaudeProvider("", "")}
	sp.loadExistingLines("s1", offset)
	if got := sp.ReplayedTurnEnd(); got != TurnEndNone {
		t.Fatalf("ReplayedTurnEnd = %v, want none (cleared)", got)
	}
}

// recoverFixture starts a fake live holder for a session recorded as
// `status`, whose stream file holds `lines`, and runs RecoverStreamProcesses
// so the manager reconnects to it (the path taken after a daemon restart).
type recoverFixture struct {
	m       *Manager
	id      string
	conn    net.Conn
	mu      sync.Mutex
	changes []Status
}

func newRecoverFixture(t *testing.T, status Status, lines ...string) *recoverFixture {
	t.Helper()
	t.Setenv("TMPDIR", shortTempDir(t))
	t.Setenv("HOME", t.TempDir())

	proc, err := os.StartProcess("/bin/sleep", []string{"sleep", "60"}, &os.ProcAttr{})
	if err != nil {
		t.Skip("cannot start test process:", err)
	}
	t.Cleanup(func() {
		_ = proc.Kill()
		_, _ = proc.Wait()
	})

	f := &recoverFixture{id: "rec1"}
	srv := newFakeHolderServer(t, f.id, proc.Pid)
	t.Cleanup(srv.close)
	writeRecoverStream(t, f.id, lines...)

	sqlDB := newTestDB(t)
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	now := time.Now()
	if _, err := sqlDB.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.id, "rec", "/tmp", "", string(status), "worker", "stream", "claude", "opus", "cli-1", proc.Pid, 0, now, now,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	f.m = &Manager{
		db:              sqlDB,
		logger:          slog.Default(),
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
	}
	f.m.SetOnStatusChange(func(_ string, s Status, _ string) {
		f.mu.Lock()
		f.changes = append(f.changes, s)
		f.mu.Unlock()
	})

	f.m.RecoverStreamProcesses()
	select {
	case f.conn = <-srv.acceptCh:
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not reconnect to the live holder")
	}
	t.Cleanup(func() {
		f.m.streamMu.Lock()
		sp := f.m.streamProcesses[f.id]
		f.m.streamMu.Unlock()
		if sp != nil {
			<-sp.Disconnect()
		}
		f.conn.Close()
	})
	return f
}

func (f *recoverFixture) status(t *testing.T) (Status, bool) {
	t.Helper()
	var s string
	var started int
	if err := f.m.db.QueryRow("SELECT status, conversation_started FROM sessions WHERE id = ?", f.id).Scan(&s, &started); err != nil {
		t.Fatal(err)
	}
	return Status(s), started != 0
}

func (f *recoverFixture) idleChanges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.changes {
		if s == StatusIdle {
			n++
		}
	}
	return n
}

// TestRecover_ResultWrittenWhileDaemonDown_SetsIdle reproduces #855: the turn
// ended while the daemon was restarting, so the result event exists only in
// the stream file. Reconnecting must apply the turn end.
func TestRecover_ResultWrittenWhileDaemonDown_SetsIdle(t *testing.T) {
	f := newRecoverFixture(t, StatusWorking, recInit, recUser, recAssistant, recResult)
	st, started := f.status(t)
	if st != StatusIdle {
		t.Fatalf("status = %s, want idle", st)
	}
	if !started {
		t.Error("conversation_started was not set by the replayed turn end")
	}
	if n := f.idleChanges(); n != 1 {
		t.Errorf("idle status changes = %d, want 1", n)
	}
}

// TestRecover_ReplayedResultAlsoReadFromSocket_HandledOnce covers the race
// where the holder wrote the result to the file after the daemon connected:
// the same line is loaded from the file and then read from the socket.
func TestRecover_ReplayedResultAlsoReadFromSocket_HandledOnce(t *testing.T) {
	f := newRecoverFixture(t, StatusWorking, recUser, recAssistant, recResult)
	if _, err := fmt.Fprintf(f.conn, "%s\n", recResult); err != nil {
		t.Fatal(err)
	}
	// A following, different turn end must still be handled live.
	second := `{"type":"result","subtype":"success","session_id":"cli-1","uuid":"r-2"}`
	if _, err := fmt.Fprintf(f.conn, "%s\n%s\n", recUser, second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.idleChanges() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := f.idleChanges(); n != 2 {
		t.Fatalf("idle status changes = %d, want 2 (replayed once + the next turn)", n)
	}
}

func TestRecover_TurnStillInProgress_StaysWorking(t *testing.T) {
	f := newRecoverFixture(t, StatusWorking, recUser, recAssistant, recResult, recUser)
	if st, _ := f.status(t); st != StatusWorking {
		t.Fatalf("status = %s, want working", st)
	}
	// The live result still ends the turn.
	if _, err := fmt.Fprintf(f.conn, "%s\n", recResult); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.idleChanges() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st, _ := f.status(t); st != StatusIdle {
		t.Fatalf("status after live result = %s, want idle", st)
	}
}

func TestRecover_NotWorking_NoStatusChange(t *testing.T) {
	f := newRecoverFixture(t, StatusWaitingInput, recUser, recAssistant, recResult)
	if st, _ := f.status(t); st != StatusWaitingInput {
		t.Fatalf("status = %s, want waiting_input (untouched)", st)
	}
	if n := f.idleChanges(); n != 0 {
		t.Errorf("idle status changes = %d, want 0", n)
	}
}
