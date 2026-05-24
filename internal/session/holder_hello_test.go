package session

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Regression tests for #656: the daemon used to race the old holder's
// 100ms shutdown window when restartForCodex or recover called ConnectToHolder
// on the same socket path, sometimes binding to the dying old listener and
// then silently losing all events from the new holder.
//
// The fix is a hello handshake: every holder writes a control message with
// its own PID immediately after accept, and the daemon verifies the PID
// before trusting the connection.

// shortTempDir returns a short-enough tmpdir so that
// $TMPDIR/agmux/socks/<sessionID>.sock stays under the macOS 104-byte unix
// socket path limit. t.TempDir() returns ~80 bytes which is too long once
// the socket suffix is appended.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agmtest-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fakeHolderServer is a minimal listener that mimics RunHolder's accept
// behavior for tests. It writes a configurable hello on accept and then
// optionally broadcasts canned events.
type fakeHolderServer struct {
	listener   net.Listener
	pid        int
	acceptCh   chan net.Conn
	wantClosed atomic.Bool
}

func newFakeHolderServer(t *testing.T, sessionID string, pid int) *fakeHolderServer {
	t.Helper()
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	sockPath := SocketPath(sessionID)
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &fakeHolderServer{
		listener: l,
		pid:      pid,
		acceptCh: make(chan net.Conn, 4),
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			// Mimic holder.go acceptLoop: send hello first.
			hello, _ := json.Marshal(HolderControlMessage{
				Type:  "control",
				Event: "hello",
				PID:   srv.pid,
			})
			_, _ = fmt.Fprintf(conn, "%s\n", hello)
			srv.acceptCh <- conn
		}
	}()
	return srv
}

func (s *fakeHolderServer) close() {
	s.wantClosed.Store(true)
	_ = s.listener.Close()
}

// TestConnectToHolderExpectingPID_MatchingPID verifies the happy path: the
// holder sends a hello with the expected PID and the connection is returned.
func TestConnectToHolderExpectingPID_MatchingPID(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "match-pid"
	srv := newFakeHolderServer(t, sessionID, 4242)
	defer srv.close()

	conn, err := ConnectToHolderExpectingPID(sessionID, 4242)
	if err != nil {
		t.Fatalf("ConnectToHolderExpectingPID: %v", err)
	}
	defer conn.Close()
}

// TestConnectToHolderExpectingPID_MismatchedPID_Retries verifies that when
// the first accepted connection responds with the wrong PID (simulating the
// race in #656 where the dying old holder accepts our dial), the function
// closes that conn and retries until it lands on a holder whose PID matches.
//
// We swap the underlying server out partway through to simulate the takeover.
func TestConnectToHolderExpectingPID_MismatchedPID_Retries(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "mismatch-then-match"
	// First server reports the WRONG PID (simulating the dying old holder).
	srv1 := newFakeHolderServer(t, sessionID, 1111)

	// In a goroutine, after a short delay, kill srv1 and start srv2 with
	// the EXPECTED PID. This simulates the new holder taking over the
	// socket path.
	const expectedPID = 9999
	const swapAfter = 250 * time.Millisecond
	go func() {
		time.Sleep(swapAfter)
		srv1.close()
		// Recreate the listener with the expected PID.
		_ = newFakeHolderServer(t, sessionID, expectedPID)
	}()

	start := time.Now()
	conn, err := ConnectToHolderExpectingPID(sessionID, expectedPID)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ConnectToHolderExpectingPID: %v", err)
	}
	defer conn.Close()

	// Sanity: the function should have retried until srv2 was ready, so
	// elapsed must be at least swapAfter. If it returned earlier the PID
	// check is not enforced.
	if elapsed < swapAfter {
		t.Errorf("returned in %v, but expected >= %v (swap time) — PID check may not be enforced", elapsed, swapAfter)
	}
}

// TestConnectToHolderExpectingPID_ZeroPID_AcceptsAny verifies that passing
// expectedPID=0 accepts any holder that sends a hello.
func TestConnectToHolderExpectingPID_ZeroPID_AcceptsAny(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "zero-pid"
	srv := newFakeHolderServer(t, sessionID, 7777)
	defer srv.close()

	conn, err := ConnectToHolderExpectingPID(sessionID, 0)
	if err != nil {
		t.Fatalf("ConnectToHolderExpectingPID(0): %v", err)
	}
	defer conn.Close()
}

// TestConnectToHolderExpectingPID_PreHelloHolder_FallsBack verifies backward
// compat on the recover path (expectedPID == 0): if the holder is an older
// binary that never sends a hello (silent after accept), the function
// eventually returns the connection so the daemon can still receive
// subsequent broadcasts. The fallback uses the short read deadline to
// detect the silent peer.
//
// Important: fallback is only valid when expectedPID == 0. The spawn /
// restart path (expectedPID > 0) intentionally does NOT fall back — see
// TestConnectToHolderExpectingPID_NoFallbackWhenPIDExpected.
func TestConnectToHolderExpectingPID_PreHelloHolder_FallsBack(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "pre-hello"
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := SocketPath(sessionID)
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	// Accept connections but never write anything (simulating old holder).
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			// Hold the conn open; the test will close it via the deferred close.
			_ = c
		}
	}()

	start := time.Now()
	// expectedPID == 0 = recover path: fallback is allowed.
	conn, err := ConnectToHolderExpectingPID(sessionID, 0)
	if err != nil {
		t.Fatalf("expected fallback success on recover path, got error: %v", err)
	}
	defer conn.Close()

	elapsed := time.Since(start)
	if elapsed < 2*time.Second {
		t.Errorf("fallback returned too quickly (%v); expected >= ~2s read deadline", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("fallback took too long (%v); deadline should be ~2s + a bit", elapsed)
	}
}

// TestHolderHelloProtocol_NoOverReadIntoSubsequentBroadcast simulates the
// real race condition: a fake holder sends hello and then immediately
// broadcasts a JSONL event. The daemon must consume only the hello and the
// next reader on the conn must see the event intact (no bytes lost).
//
// This regression test guards against using bufio.Reader inside the hello
// handshake — bufio.Reader would over-read past the newline and stash bytes
// in its private buffer that the caller's own bufio.Reader would never see,
// silently dropping the first broadcast event.
func TestHolderHelloProtocol_NoOverReadIntoSubsequentBroadcast(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "no-over-read"
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := SocketPath(sessionID)
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	const pid = 5555
	const firstEvent = `{"type":"system","subtype":"init","session_id":"abc","model":"test-model"}`

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		// Send hello immediately followed by an event in a single write,
		// maximizing the chance of over-read on the consumer side.
		hello, _ := json.Marshal(HolderControlMessage{
			Type:  "control",
			Event: "hello",
			PID:   pid,
		})
		_, _ = fmt.Fprintf(conn, "%s\n%s\n", hello, firstEvent)
	}()

	conn, err := ConnectToHolderExpectingPID(sessionID, pid)
	if err != nil {
		t.Fatalf("ConnectToHolderExpectingPID: %v", err)
	}
	defer conn.Close()

	// Read a single line from the conn using the same mechanism the readLoop
	// uses (bufio.NewReader). The next line MUST be the firstEvent.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := readLineFromConn(conn, 8192)
	if err != nil {
		t.Fatalf("read next line: %v", err)
	}
	if got != firstEvent {
		t.Errorf("first post-hello line = %q, want %q (over-read regression)", got, firstEvent)
	}
}

// TestConnectToHolderExpectingPID_NoFallbackWhenPIDExpected verifies that
// when expectedPID > 0 (= spawn or restart path), the function does NOT fall
// back to "assume pre-hello holder" if the hello never arrives. Falling back
// in that case would silently re-open the very race window #656 fixes.
//
// We simulate a silent holder (accepts but never writes) and assert that
// ConnectToHolderExpectingPID returns an error before the overall 15s
// deadline expires (it should keep retrying on each accept and eventually
// time out).
func TestConnectToHolderExpectingPID_NoFallbackWhenPIDExpected(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "no-fallback-when-pid-expected"
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := SocketPath(sessionID)
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	// Accept connections but never write anything (simulates a stuck/silent
	// or dying old holder that won't speak the hello protocol).
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c // hold open
		}
	}()

	// Use a short overall deadline by capping with t.Deadline if any; here we
	// just rely on the function's internal 15s deadline. To keep CI fast we
	// abort the test if it doesn't return within 20s.
	done := make(chan error, 1)
	go func() {
		_, e := ConnectToHolderExpectingPID(sessionID, 4242)
		done <- e
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error when expectedPID>0 and no hello arrives, got nil (silent fallback regression)")
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("ConnectToHolderExpectingPID did not return within 20s")
	}
}

// TestConnectToHolderExpectingPID_NoFallbackOnMalformedHello verifies that
// a malformed (non-hello) first line is also NOT silently accepted when
// expectedPID > 0. This mirrors the timeout case above.
func TestConnectToHolderExpectingPID_NoFallbackOnMalformedHello(t *testing.T) {
	t.Setenv("TMPDIR", shortTempDir(t))

	sessionID := "no-fallback-on-malformed"
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := SocketPath(sessionID)
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	// Accept and write a non-hello line (e.g. a stale broadcast).
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = fmt.Fprintln(c, `{"type":"system","subtype":"init"}`)
			// hold the conn open so we don't get EOF before our read
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, e := ConnectToHolderExpectingPID(sessionID, 5555)
		done <- e
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error when expectedPID>0 and first line is not hello, got nil")
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("ConnectToHolderExpectingPID did not return within 20s")
	}
}

// TestReadLineFromConn_StopsAtNewline ensures the helper does not over-read
// past the first newline. This is the core invariant the hello handshake
// depends on.
func TestReadLineFromConn_StopsAtNewline(t *testing.T) {
	// Set up a unix socketpair so we can write known bytes and read via
	// our helper.
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "tln.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	const first = `{"hello":"world"}`
	const second = `{"more":"data"}`

	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(c, "%s\n%s\n", first, second)
		// Keep c open until test finishes to avoid EOF affecting the second read.
		time.Sleep(2 * time.Second)
		_ = c.Close()
	}()

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	got1, err := readLineFromConn(c, 4096)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if got1 != first {
		t.Errorf("first = %q, want %q", got1, first)
	}

	got2, err := readLineFromConn(c, 4096)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if got2 != second {
		t.Errorf("second = %q, want %q (helper over-read past first newline)", got2, second)
	}
}
