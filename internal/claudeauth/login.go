package claudeauth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// `claude auth login` (checked with Claude Code in 2026-11) prints
//
//	Opening browser to sign in…
//	If the browser didn't open, visit: <https://claude.com/cai/oauth/authorize?code=true&...>
//	Paste code here if prompted >
//
// and accepts the code shown by https://platform.claude.com/oauth/code/callback
// on its terminal. A rejected code ends the process with
// "Couldn't log in: That code wasn't accepted. ...". LoginFlow drives that
// prompt through a PTY so the user can finish the login from a phone.
//
// The URL and the code are secrets of the login attempt: never log them.

// Process is a running `claude auth login` attached to a terminal.
type Process interface {
	// Read returns terminal output; it fails once the process has exited.
	Read(p []byte) (int, error)
	// Write types into the terminal.
	Write(p []byte) (int, error)
	// Wait waits for the process to exit (call after Read failed).
	Wait() error
	// Kill terminates the process.
	Kill() error
}

// Starter launches `claude auth login` in a PTY.
type Starter func() (Process, error)

// StatusChecker reports whether the CLI is logged in (`claude auth status`).
type StatusChecker func(ctx context.Context) (bool, error)

// LoginPhase is the state of a login attempt.
type LoginPhase string

const (
	PhaseIdle        LoginPhase = "idle"
	PhaseStarting    LoginPhase = "starting"     // waiting for the CLI to print the URL
	PhaseWaitingCode LoginPhase = "waiting_code" // URL shown, waiting for the user's code
	PhaseVerifying   LoginPhase = "verifying"    // code sent, waiting for the CLI to finish
	PhaseSucceeded   LoginPhase = "succeeded"
	PhaseFailed      LoginPhase = "failed"
)

func (p LoginPhase) active() bool {
	return p == PhaseStarting || p == PhaseWaitingCode || p == PhaseVerifying
}

// LoginStatus is the public view of the current attempt.
type LoginStatus struct {
	Phase     LoginPhase `json:"phase"`
	URL       string     `json:"url,omitempty"` // only while waiting for the code
	Message   string     `json:"message,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

var (
	ErrBusy       = errors.New("a login is already in progress")
	ErrNotWaiting = errors.New("no login is waiting for a code")
	ErrBadCode    = errors.New("invalid code")
)

// DefaultLoginTimeout bounds one login attempt; the PTY is killed after it.
const DefaultLoginTimeout = 10 * time.Minute

// LoginFlow runs at most one login attempt at a time.
type LoginFlow struct {
	start     Starter
	check     StatusChecker
	onSuccess func()

	// Token mode (`claude setup-token`, see token.go): the attempt ends when
	// the CLI prints the token, which is handed to saveToken instead of
	// being checked with check.
	token     bool
	saveToken func(token string) error
	label     string // the command, for messages

	// SubmitDelay, when set, separates the code from the Enter key: the
	// setup-token screen takes a code typed together with "\r" as a paste
	// and does not submit it.
	SubmitDelay time.Duration

	// Timeout is the lifetime of one attempt (default DefaultLoginTimeout).
	Timeout time.Duration
	// URLWait bounds how long Start waits for the URL (default 30s).
	URLWait time.Duration
	// VerifyTimeout bounds the CLI's exit after the code is sent (default 2m).
	VerifyTimeout time.Duration

	now func() time.Time

	mu        sync.Mutex
	gen       int
	phase     LoginPhase
	url       string
	message   string
	startedAt time.Time
	expiresAt time.Time
	proc      Process
	timer     *time.Timer
	output    []byte
	urlReady  chan struct{}
}

// NewLoginFlow creates a flow. onSuccess runs after the CLI confirms the login.
func NewLoginFlow(start Starter, check StatusChecker, onSuccess func()) *LoginFlow {
	return &LoginFlow{start: start, check: check, onSuccess: onSuccess, phase: PhaseIdle, now: time.Now, label: "claude auth login"}
}

func (f *LoginFlow) timeout() time.Duration {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return DefaultLoginTimeout
}

// Status returns the current attempt.
func (f *LoginFlow) Status() LoginStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusLocked()
}

func (f *LoginFlow) statusLocked() LoginStatus {
	st := LoginStatus{Phase: f.phase, Message: f.message}
	if f.phase == PhaseWaitingCode {
		st.URL = f.url
	}
	if !f.startedAt.IsZero() {
		s, e := f.startedAt, f.expiresAt
		st.StartedAt, st.ExpiresAt = &s, &e
	}
	return st
}

// Start launches `claude auth login` and waits until it prints the URL.
func (f *LoginFlow) Start(ctx context.Context) (LoginStatus, error) {
	f.mu.Lock()
	if f.phase.active() {
		st := f.statusLocked()
		f.mu.Unlock()
		return st, ErrBusy
	}
	proc, err := f.start()
	if err != nil {
		f.phase, f.message, f.url = PhaseFailed, f.label+" を起動できませんでした: "+err.Error(), ""
		st := f.statusLocked()
		f.mu.Unlock()
		return st, nil
	}
	f.gen++
	gen := f.gen
	f.phase, f.message, f.url, f.output = PhaseStarting, "", "", nil
	f.startedAt = f.now()
	f.expiresAt = f.startedAt.Add(f.timeout())
	f.proc = proc
	f.urlReady = make(chan struct{})
	urlReady := f.urlReady
	f.timer = time.AfterFunc(f.timeout(), func() {
		f.abort(gen, "時間切れになりました。もう一度やり直してください。")
	})
	f.mu.Unlock()

	go f.readLoop(gen, proc)

	wait := f.URLWait
	if wait <= 0 {
		wait = 30 * time.Second
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-urlReady:
	case <-t.C:
		f.abort(gen, f.label+" が認証 URL を出しませんでした。")
	case <-ctx.Done():
	}
	return f.Status(), nil
}

// SubmitCode types the code into the waiting CLI.
func (f *LoginFlow) SubmitCode(code string) (LoginStatus, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\r\n\x00\x03\x04\x1b") {
		return f.Status(), ErrBadCode
	}
	f.mu.Lock()
	if f.phase != PhaseWaitingCode {
		st := f.statusLocked()
		f.mu.Unlock()
		return st, ErrNotWaiting
	}
	proc := f.proc
	gen := f.gen
	f.phase = PhaseVerifying
	f.url = ""
	f.mu.Unlock()

	if err := f.typeCode(proc, code); err != nil {
		f.abort(gen, "コードを送れませんでした。もう一度やり直してください。")
		return f.Status(), nil
	}
	vt := f.VerifyTimeout
	if vt <= 0 {
		vt = 2 * time.Minute
	}
	time.AfterFunc(vt, func() {
		f.mu.Lock()
		stuck := f.gen == gen && f.phase == PhaseVerifying
		f.mu.Unlock()
		if stuck {
			msg := "ログインの完了を確認できませんでした。もう一度やり直してください。"
			if f.token {
				msg = TokenUnreadableMessage
			}
			f.abort(gen, msg)
		}
	})
	return f.Status(), nil
}

func (f *LoginFlow) typeCode(proc Process, code string) error {
	if f.SubmitDelay <= 0 {
		_, err := proc.Write([]byte(code + "\r"))
		return err
	}
	if _, err := proc.Write([]byte(code)); err != nil {
		return err
	}
	time.Sleep(f.SubmitDelay)
	_, err := proc.Write([]byte("\r"))
	return err
}

// Cancel stops the current attempt.
func (f *LoginFlow) Cancel() LoginStatus {
	f.mu.Lock()
	gen, active := f.gen, f.phase.active()
	f.mu.Unlock()
	if active {
		f.abort(gen, "キャンセルしました。")
	}
	return f.Status()
}

// abort ends attempt gen as failed and kills its process.
func (f *LoginFlow) abort(gen int, msg string) {
	f.mu.Lock()
	if f.gen != gen || !f.phase.active() {
		f.mu.Unlock()
		return
	}
	f.phase, f.message, f.url = PhaseFailed, msg, ""
	proc := f.endLocked()
	f.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
}

// endLocked releases the attempt's resources; the caller kills the process.
func (f *LoginFlow) endLocked() Process {
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	if f.urlReady != nil {
		close(f.urlReady)
		f.urlReady = nil
	}
	proc := f.proc
	f.proc = nil
	f.output = nil
	return proc
}

const maxOutput = 64 << 10

func (f *LoginFlow) readLoop(gen int, proc Process) {
	buf := make([]byte, 4096)
	decided := false // token mode: the result is known; only drain the PTY
	for {
		n, err := proc.Read(buf)
		if n > 0 && !decided {
			var tok, fail string
			f.mu.Lock()
			if f.gen == gen {
				f.output = append(f.output, buf[:n]...)
				if len(f.output) > maxOutput {
					f.output = f.output[len(f.output)-maxOutput:]
				}
				if f.phase == PhaseStarting {
					if u := ExtractLoginURL(string(f.output)); u != "" {
						f.url = u
						f.phase = PhaseWaitingCode
						if f.urlReady != nil {
							close(f.urlReady)
							f.urlReady = nil
						}
					}
				}
				if f.token && f.phase == PhaseVerifying {
					tok, fail = scanSetupToken(string(f.output))
				}
			}
			f.mu.Unlock()
			if tok != "" || fail != "" {
				// Done: the CLI may wait for a key press (e.g. "Press
				// Enter to retry."), so kill it instead of waiting. Keep
				// reading until the PTY closes: on macOS a process cannot
				// finish exiting while its terminal output is not drained.
				decided = true
				_ = proc.Kill()
				f.finishToken(gen, tok, fail)
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := proc.Wait()
	if !decided {
		f.finish(gen, waitErr)
	}
}

// finish handles the CLI's exit.
func (f *LoginFlow) finish(gen int, waitErr error) {
	f.mu.Lock()
	if f.gen != gen || !f.phase.active() {
		f.mu.Unlock()
		return
	}
	out := string(f.output)
	if f.token {
		f.mu.Unlock()
		tok, fail := ExtractSetupToken(out), ""
		if tok == "" {
			fail = TokenUnreadableMessage
			if m := setupTokenFailure(out); m != "" {
				fail = m
			}
		}
		f.finishToken(gen, tok, fail)
		return
	}
	if waitErr != nil {
		f.phase, f.url = PhaseFailed, ""
		f.message = failureMessage(out, f.label)
		f.endLocked()
		f.mu.Unlock()
		return
	}
	f.phase, f.url = PhaseVerifying, ""
	f.mu.Unlock()

	ok := false
	if f.check != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		loggedIn, err := f.check(ctx)
		cancel()
		ok = err == nil && loggedIn
	}

	// The login is real once the CLI confirms it, whatever happens to this
	// attempt meanwhile. Run onSuccess before publishing "succeeded" so a
	// client that sees the phase also sees its effect.
	if ok && f.onSuccess != nil {
		f.onSuccess()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gen != gen || f.phase != PhaseVerifying {
		return
	}
	if ok {
		f.phase, f.message = PhaseSucceeded, "ログインしました。"
	} else {
		f.phase, f.message = PhaseFailed, "claude auth login は終了しましたが、ログイン状態を確認できませんでした。"
	}
	f.endLocked()
}

var (
	ansiRe     = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]`)
	loginURLRe = regexp.MustCompile(`https://[^\s\x07\x1b"'<>]+/oauth/authorize\?[^\s\x07\x1b"'<>]+`)
)

// StripANSI removes terminal escape sequences (including OSC 8 hyperlinks).
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// ExtractLoginURL finds the manual-login URL in the CLI's output.
func ExtractLoginURL(out string) string {
	return loginURLRe.FindString(StripANSI(out))
}

// failureMessage picks the CLI's own explanation of a failed login. It never
// returns the line the code was echoed on, nor anything with a URL.
func failureMessage(out, label string) string {
	for _, l := range strings.Split(StripANSI(out), "\n") {
		l = strings.TrimSpace(strings.ReplaceAll(l, "\r", ""))
		if strings.HasPrefix(l, "Couldn't log in") && !strings.Contains(l, "http") {
			return l
		}
	}
	return label + " が失敗しました。もう一度やり直してください。"
}
