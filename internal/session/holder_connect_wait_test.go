package session

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func shortenHolderConnectTimeouts(t *testing.T, base, max time.Duration) {
	t.Helper()
	origBase, origMax := holderConnectTimeout, holderConnectMaxTimeout
	holderConnectTimeout, holderConnectMaxTimeout = base, max
	t.Cleanup(func() {
		holderConnectTimeout, holderConnectMaxTimeout = origBase, origMax
	})
}

// A holder that is alive but slow to start listening (e.g. under memory
// pressure) must not be given up on at the base timeout.
func TestConnectToHolderExpectingPID_WaitsLongerWhileHolderAlive(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))
	shortenHolderConnectTimeouts(t, 300*time.Millisecond, 5*time.Second)

	// A live child process stands in for the slow holder.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	pid := child.Process.Pid

	sessionID := "slowstart"
	srvCh := make(chan *fakeHolderServer, 1)
	go func() {
		time.Sleep(1 * time.Second) // well past the base timeout
		srvCh <- newFakeHolderServer(t, sessionID, pid)
	}()
	t.Cleanup(func() {
		select {
		case s := <-srvCh:
			s.close()
		default:
		}
	})

	conn, err := ConnectToHolderExpectingPID(sessionID, pid)
	if err != nil {
		t.Fatalf("expected to connect to slow-starting holder, got: %v", err)
	}
	conn.Close()
}

// A holder that already exited will never listen; fail without waiting for
// the timeout.
func TestConnectToHolderExpectingPID_FailsFastWhenHolderExited(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))
	shortenHolderConnectTimeouts(t, 10*time.Second, 20*time.Second)

	child := exec.Command("sh", "-c", "exit 3")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := child.Process.Pid
	_ = child.Process.Release() // same as SpawnHolder: no one Waits on it

	start := time.Now()
	_, err := ConnectToHolderExpectingPID("exitedholder", pid)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "holder exited before listening") || !strings.Contains(err.Error(), "exit code 3") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("should fail fast, took %v", elapsed)
	}
}

// Without a live holder process the base timeout still applies.
func TestConnectToHolderExpectingPID_NoExtensionWhenHolderNotAlive(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))
	shortenHolderConnectTimeouts(t, 300*time.Millisecond, 10*time.Second)

	start := time.Now()
	_, err := ConnectToHolderExpectingPID("nosuchholder", 0x7ffffff0)
	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("should not extend wait for a dead pid, took %v", elapsed)
	}
}

// A holder tracked by trackHolderExit (as SpawnHolder does) must be reaped
// once it exits, so it does not linger as a zombie, while reapIfExited still
// reports its exit status for the connect fail-fast path.
func TestTrackHolderExit_ReapsZombieAndKeepsStatus(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))
	shortenHolderConnectTimeouts(t, 10*time.Second, 20*time.Second)

	child := exec.Command("sh", "-c", "exit 3")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := child.Process.Pid
	trackHolderExit(child.Process)

	v, ok := spawnedHolderExits.Load(pid)
	if !ok {
		t.Fatal("exit record not stored")
	}
	select {
	case <-v.(*holderExit).done:
	case <-time.After(5 * time.Second):
		t.Fatal("holder was not reaped")
	}

	// Already reaped: no zombie remains for this pid.
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil); err != syscall.ECHILD {
		t.Fatalf("expected ECHILD after reap, got %v", err)
	}

	exited, status := reapIfExited(pid)
	if !exited || status != "exit code 3" {
		t.Fatalf("reapIfExited = %v, %q", exited, status)
	}

	_, err := ConnectToHolderExpectingPID("trackedholder", pid)
	if err == nil || !strings.Contains(err.Error(), "holder exited before listening") || !strings.Contains(err.Error(), "exit code 3") {
		t.Fatalf("unexpected error: %v", err)
	}
}
