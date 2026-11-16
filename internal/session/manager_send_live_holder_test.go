package session

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestSendKeys_LiveHolderWithoutStreamProcess_AttachesInsteadOfKilling is a
// regression test for sends to a session that is in the middle of a turn
// from a Manager that has no stream process for it (`agmux session send`
// used to run an in-process Manager). The auto-recover path called
// killStaleHolder, which SIGTERMs the live holder, waits 5s and SIGKILLs
// its process group while the CLI was still working, then spawned a new
// holder. The message must instead be delivered over the live holder's
// socket, and the holder must survive.
func TestSendKeys_LiveHolderWithoutStreamProcess_AttachesInsteadOfKilling(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))
	t.Setenv("HOME", t.TempDir())

	// Stand-in for the holder process: alive for the whole test.
	proc, err := os.StartProcess("/bin/sleep", []string{"sleep", "60"}, &os.ProcAttr{})
	if err != nil {
		t.Skip("cannot start test process:", err)
	}
	holderPID := proc.Pid
	t.Cleanup(func() {
		_ = proc.Kill()
		_, _ = proc.Wait()
	})

	id := "sendlive"
	srv := newFakeHolderServer(t, id, holderPID)
	defer srv.close()

	db := newTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(1) // :memory: is per-connection
	now := time.Now()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "live", "/tmp", "", "working", "worker", "stream", "claude", "", "cli-uuid", holderPID, 1, now, now,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	m := &Manager{
		db:              db,
		logger:          slog.Default(),
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- m.SendKeys(id, "queued while working") }()

	var conn interface {
		Read([]byte) (int, error)
		SetReadDeadline(time.Time) error
		Close() error
	}
	select {
	case c := <-srv.acceptCh:
		conn = c
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not connect to the live holder")
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read message from manager: %v", err)
	}
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if msg.Type != "user" || msg.Message.Content != "queued while working" {
		t.Errorf("holder got %q, want user message %q", line, "queued while working")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendKeys: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendKeys did not return")
	}

	if !IsHolderAlive(holderPID) {
		t.Fatal("live holder was killed by SendKeys")
	}
	m.streamMu.Lock()
	sp, ok := m.streamProcesses[id]
	m.streamMu.Unlock()
	if !ok || sp.HolderPID() != holderPID {
		t.Errorf("stream process not registered for the live holder (ok=%v)", ok)
	}
	var gotPID int
	if err := db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", id).Scan(&gotPID); err != nil {
		t.Fatalf("query holder_pid: %v", err)
	}
	if gotPID != holderPID {
		t.Errorf("holder_pid = %d, want unchanged %d", gotPID, holderPID)
	}
}
