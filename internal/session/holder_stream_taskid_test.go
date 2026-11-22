package session

import (
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestReadLoop_TaskIDReuseDecrementsRunningTasks verifies that when the same
// task ID is started again after completing (a resumed subagent), its second
// completion is still counted, so runningTasks returns to 0 and the following
// result fires onTurnComplete (#866).
func TestReadLoop_TaskIDReuseDecrementsRunningTasks(t *testing.T) {
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "rl-reuse.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	serverDone := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		serverDone <- c
	}()
	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn := <-serverDone
	if serverConn == nil {
		t.Fatal("accept failed")
	}

	var completes atomic.Int32
	sp := &HolderStreamProcess{
		conn:       clientConn,
		done:       make(chan struct{}),
		provider:   NewClaudeProvider("", ""),
		streamOpts: StreamOpts{SessionID: "task-id-reuse"},
		stopped:    true,
		onTurnComplete: func(sid string) {
			completes.Add(1)
		},
	}
	go sp.readLoop()

	lines := []string{
		`{"type":"system","subtype":"task_started","task_id":"X"}`,
		`{"type":"system","subtype":"task_notification","task_id":"X","status":"completed"}`,
		`{"type":"result","subtype":"success"}`,
		`{"type":"system","subtype":"task_started","task_id":"X"}`,
		`{"type":"system","subtype":"task_notification","task_id":"X","status":"completed"}`,
		`{"type":"result","subtype":"success"}`,
	}
	for _, ln := range lines {
		if _, err := serverConn.Write([]byte(ln + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = serverConn.Close()

	select {
	case <-sp.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("readLoop did not exit within 3s")
	}

	sp.mu.RLock()
	running := sp.runningTasks
	sp.mu.RUnlock()
	if running != 0 {
		t.Errorf("runningTasks = %d, want 0", running)
	}
	if got := completes.Load(); got != 2 {
		t.Errorf("onTurnComplete fired %d times, want 2", got)
	}
}
