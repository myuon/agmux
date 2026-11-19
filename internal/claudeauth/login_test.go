package claudeauth

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=s"

// fakeProc emulates `claude auth login` on a terminal.
type fakeProc struct {
	outR *io.PipeReader
	outW *io.PipeWriter

	mu      sync.Mutex
	written strings.Builder
	exitErr error
	exited  chan struct{}
	once    sync.Once
	killed  bool
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{outR: r, outW: w, exited: make(chan struct{})}
}

func (p *fakeProc) Read(b []byte) (int, error) { return p.outR.Read(b) }
func (p *fakeProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written.Write(b)
	return len(b), nil
}
func (p *fakeProc) Wait() error {
	<-p.exited
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}
func (p *fakeProc) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.exit(errors.New("killed"))
	return nil
}
func (p *fakeProc) print(s string) { _, _ = p.outW.Write([]byte(s)) }
func (p *fakeProc) exit(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.exitErr = err
		p.mu.Unlock()
		_ = p.outW.Close()
		close(p.exited)
	})
}
func (p *fakeProc) typed() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written.String()
}
func (p *fakeProc) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

func printPrompt(p *fakeProc) {
	p.print("Opening browser to sign in…\r\nIf the browser didn't open, visit: \x1b]8;;" + testURL + "\x07" + testURL + "\x1b]8;;\x07\r\nPaste code here if prompted > ")
}

func waitPhase(t *testing.T, f *LoginFlow, want LoginPhase) LoginStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := f.Status(); st.Phase == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("phase did not become %s (now %s)", want, f.Status().Phase)
	return LoginStatus{}
}

func TestLoginFlow_Success(t *testing.T) {
	proc := newFakeProc()
	var succeeded bool
	f := NewLoginFlow(
		func() (Process, error) { go printPrompt(proc); return proc, nil },
		func(context.Context) (bool, error) { return true, nil },
		func() { succeeded = true },
	)

	st, err := f.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, PhaseWaitingCode, st.Phase)
	assert.Equal(t, testURL, st.URL)
	require.NotNil(t, st.ExpiresAt)

	// Only one attempt at a time.
	_, err = f.Start(context.Background())
	assert.ErrorIs(t, err, ErrBusy)

	st, err = f.SubmitCode("  abc#def \n")
	require.NoError(t, err)
	assert.Equal(t, PhaseVerifying, st.Phase)
	assert.Empty(t, st.URL, "the URL is not exposed after the code was sent")
	assert.Equal(t, "abc#def\r", proc.typed())

	proc.print("Signing in…\r\nLogin successful.\r\n")
	proc.exit(nil)
	st = waitPhase(t, f, PhaseSucceeded)
	assert.True(t, succeeded)
	assert.Empty(t, st.URL)

	// A new attempt is allowed once the previous one ended.
	proc = newFakeProc()
	_, err = f.Start(context.Background())
	require.NoError(t, err)
	f.Cancel()
}

func TestLoginFlow_RejectedCode(t *testing.T) {
	proc := newFakeProc()
	succeeded := false
	f := NewLoginFlow(
		func() (Process, error) { go printPrompt(proc); return proc, nil },
		func(context.Context) (bool, error) { return true, nil },
		func() { succeeded = true },
	)
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("secretcode#state")
	require.NoError(t, err)
	proc.print("secretcode#state\r\r\nSigning in…\r\nCouldn't log in: That code wasn't accepted. Sign in again to get a new code.\r\n")
	proc.exit(errors.New("exit status 1"))

	st := waitPhase(t, f, PhaseFailed)
	assert.False(t, succeeded)
	assert.Contains(t, st.Message, "Couldn't log in")
	assert.NotContains(t, st.Message, "secretcode")
}

func TestLoginFlow_ExitOKButNotLoggedIn(t *testing.T) {
	proc := newFakeProc()
	f := NewLoginFlow(
		func() (Process, error) { go printPrompt(proc); return proc, nil },
		func(context.Context) (bool, error) { return false, nil },
		func() { t.Error("onSuccess must not run") },
	)
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("abc")
	require.NoError(t, err)
	proc.exit(nil)
	waitPhase(t, f, PhaseFailed)
}

func TestLoginFlow_CodeValidationAndState(t *testing.T) {
	proc := newFakeProc()
	f := NewLoginFlow(func() (Process, error) { go printPrompt(proc); return proc, nil }, nil, nil)

	_, err := f.SubmitCode("abc")
	assert.ErrorIs(t, err, ErrNotWaiting, "no attempt yet")

	_, err = f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("")
	assert.ErrorIs(t, err, ErrBadCode)
	_, err = f.SubmitCode("a\x03b")
	assert.ErrorIs(t, err, ErrBadCode)
	assert.Equal(t, "", proc.typed())
	f.Cancel()
	assert.True(t, proc.wasKilled())
	assert.Equal(t, PhaseFailed, f.Status().Phase)
}

func TestLoginFlow_Timeout(t *testing.T) {
	proc := newFakeProc()
	f := NewLoginFlow(func() (Process, error) { go printPrompt(proc); return proc, nil }, nil, nil)
	f.Timeout = 50 * time.Millisecond
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	st := waitPhase(t, f, PhaseFailed)
	assert.Contains(t, st.Message, "時間切れ")
	assert.True(t, proc.wasKilled())
}

func TestLoginFlow_NoURL(t *testing.T) {
	proc := newFakeProc()
	f := NewLoginFlow(func() (Process, error) { go proc.print("Opening browser to sign in…\r\n"); return proc, nil }, nil, nil)
	f.URLWait = 50 * time.Millisecond
	st, err := f.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, PhaseFailed, st.Phase)
	assert.True(t, proc.wasKilled())
}

func TestLoginFlow_StartError(t *testing.T) {
	f := NewLoginFlow(func() (Process, error) { return nil, errors.New("no claude") }, nil, nil)
	st, err := f.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, PhaseFailed, st.Phase)
}
