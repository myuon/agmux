// Package browser manages a dedicated, headed Chrome instance ("remote
// browser") and relays a screencast/input channel for one user-facing tab over
// the Chrome DevTools Protocol.
//
// Security notes: the CDP port is bound to 127.0.0.1 only; frames, typed text
// and cookies are never logged.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrChromeNotFound is returned when the Chrome executable does not exist.
var ErrChromeNotFound = errors.New("chrome executable not found")

// ErrNotRunning is returned when an operation needs a running browser.
var ErrNotRunning = errors.New("remote browser is not running")

// Config configures a Manager.
type Config struct {
	ChromePath string
	Port       int
	ProfileDir string

	// Timeouts (zero values use defaults).
	ReadyTimeout time.Duration // wait for CDP after launch (default 20s)
	StopTimeout  time.Duration // wait for Browser.close before SIGKILL (default 30s)
	RestoreGrace time.Duration // wait for session-restored tabs before creating a blank one (default 1.5s)
}

// Process is a launched Chrome process.
type Process interface {
	PID() int
	Kill() error
	Done() <-chan struct{}
}

// Launcher starts Chrome. It is injectable for tests.
type Launcher func(path string, args []string) (Process, error)

// Status describes the remote browser state.
type Status struct {
	Running      bool   `json:"running"`
	Port         int    `json:"port"`
	PID          int    `json:"pid,omitempty"`
	Owned        bool   `json:"owned"` // true when this agmux process launched it
	ProfileDir   string `json:"profileDir"`
	Version      string `json:"version,omitempty"`
	UserTabReady bool   `json:"userTabReady"`
}

// StopResult describes how a stop completed.
type StopResult struct {
	Graceful bool `json:"graceful"` // false when SIGKILL fallback was needed
}

// Manager owns (or reattaches to) one Chrome instance.
type Manager struct {
	cfg      Config
	logger   *slog.Logger
	launcher Launcher
	http     *http.Client
	inspect  func(port int) (pid int, cmdline string, err error)

	mu   sync.Mutex
	proc Process
	tab  *UserTab
}

// New creates a Manager. launcher may be nil to use the real exec launcher.
func New(cfg Config, logger *slog.Logger, launcher Launcher) *Manager {
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = 20 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 30 * time.Second
	}
	if cfg.RestoreGrace == 0 {
		cfg.RestoreGrace = 1500 * time.Millisecond
	}
	if launcher == nil {
		launcher = execLauncher
	}
	return &Manager{
		cfg:      cfg,
		logger:   logger.With("component", "remote-browser"),
		launcher: launcher,
		http:     &http.Client{Timeout: 3 * time.Second},
		inspect:  inspectListener,
	}
}

type versionInfo struct {
	Browser              string `json:"Browser"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func (m *Manager) base() string { return fmt.Sprintf("http://127.0.0.1:%d", m.cfg.Port) }

func (m *Manager) probe(ctx context.Context) (*versionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base()+"/json/version", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var v versionInfo
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	if v.WebSocketDebuggerURL == "" {
		return nil, errors.New("no browser websocket url")
	}
	return &v, nil
}

// Args returns the Chrome launch arguments.
func (c Config) Args() []string {
	return []string{
		fmt.Sprintf("--remote-debugging-port=%d", c.Port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + c.ProfileDir,
		"--restore-last-session",
		"--no-first-run",
		"--no-default-browser-check",
		// Phone-sized window, off-screen. Hidden-page throttling is handled by
		// Emulation.setFocusEmulationEnabled on the user tab.
		"--window-size=390,844",
		"--window-position=-3000,-3000",
	}
}

// Start launches Chrome, or reattaches to one already listening on the port
// that uses our profile. It is idempotent.
func (m *Manager) Start(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ver, err := m.probe(ctx); err == nil {
		// Something is already listening: reattach only if it is *our* profile.
		if err := m.verifyProfile(ctx, ver); err != nil {
			return Status{}, err
		}
		m.logger.Info("reattached to running chrome", "port", m.cfg.Port)
	} else {
		if err := m.launch(ctx); err != nil {
			return Status{}, err
		}
	}
	if err := m.ensureFirstTab(ctx); err != nil {
		return Status{}, err
	}
	return m.statusLocked(ctx), nil
}

func (m *Manager) launch(ctx context.Context) error {
	if fi, err := os.Stat(m.cfg.ChromePath); err != nil || fi.IsDir() {
		return fmt.Errorf("%w: %s (set remote_browser.chrome_path in config)", ErrChromeNotFound, m.cfg.ChromePath)
	}
	if err := os.MkdirAll(m.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}
	if err := os.Chmod(m.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("chmod profile dir: %w", err)
	}
	proc, err := m.launcher(m.cfg.ChromePath, m.cfg.Args())
	if err != nil {
		return fmt.Errorf("launch chrome: %w", err)
	}
	m.proc = proc
	go func() {
		<-proc.Done()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.proc == proc {
			m.proc = nil
			m.closeTabLocked()
		}
	}()
	m.logger.Info("launched chrome", "pid", proc.PID(), "port", m.cfg.Port)

	deadline := time.Now().Add(m.cfg.ReadyTimeout)
	for {
		if _, err := m.probe(ctx); err == nil {
			return nil
		}
		select {
		case <-proc.Done():
			return errors.New("chrome exited before becoming ready (is the profile in use by another Chrome, or the port taken?)")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			proc.Kill()
			return errors.New("timed out waiting for chrome debugging port")
		}
	}
}

// verifyProfile refuses to reattach to a Chrome that is not using our profile
// (e.g. the user's own Chrome that happens to have the same debug port).
// Browser.getBrowserCommandLine is unusable (it needs --enable-automation,
// which would flip navigator.webdriver), so the listener's command line is
// inspected instead.
func (m *Manager) verifyProfile(ctx context.Context, ver *versionInfo) error {
	_, cmdline, err := m.inspect(m.cfg.Port)
	if err != nil {
		return fmt.Errorf("port %d is in use by a browser whose profile cannot be verified: %w", m.cfg.Port, err)
	}
	want := canonPath(m.cfg.ProfileDir)
	const flag = "--user-data-dir="
	for rest := cmdline; ; {
		i := strings.Index(rest, flag)
		if i < 0 {
			break
		}
		rest = rest[i+len(flag):]
		v := rest
		if j := strings.Index(v, " --"); j >= 0 {
			v = v[:j]
		}
		if canonPath(strings.TrimSpace(v)) == want {
			return nil
		}
	}
	return fmt.Errorf("port %d is in use by a browser with a different profile; refusing to attach", m.cfg.Port)
}

// inspectListener returns the PID and command line of the process listening
// on the given loopback TCP port.
func inspectListener(port int) (int, string, error) {
	out, err := exec.Command("lsof", "-nP", "-t", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return 0, "", fmt.Errorf("lsof: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, "", errors.New("no listener found")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", err
	}
	cmd, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", fmt.Errorf("ps: %w", err)
	}
	return pid, strings.TrimSpace(string(cmd)), nil
}

func canonPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

type targetInfo struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func (m *Manager) listTargets(ctx context.Context) ([]targetInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base()+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ts []targetInfo
	if err := json.NewDecoder(resp.Body).Decode(&ts); err != nil {
		return nil, err
	}
	return ts, nil
}

func (m *Manager) pageCount(ctx context.Context) int {
	ts, err := m.listTargets(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range ts {
		if t.Type == "page" {
			n++
		}
	}
	return n
}

// ensureFirstTab guarantees a tab exists that is not the user's tab, so the
// agent's default (first) tab never aliases the user's.
func (m *Manager) ensureFirstTab(ctx context.Context) error {
	deadline := time.Now().Add(m.cfg.RestoreGrace)
	for {
		if m.pageCount(ctx) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	c, err := m.browserClient(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	return err
}

func (m *Manager) browserClient(ctx context.Context) (*cdpClient, error) {
	ver, err := m.probe(ctx)
	if err != nil {
		return nil, ErrNotRunning
	}
	return dialCDP(ctx, ver.WebSocketDebuggerURL, nil)
}

// Status reports the current state.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(ctx)
}

func (m *Manager) statusLocked(ctx context.Context) Status {
	st := Status{Port: m.cfg.Port, ProfileDir: m.cfg.ProfileDir}
	ver, err := m.probe(ctx)
	if err != nil {
		return st
	}
	st.Running = true
	st.Version = ver.Browser
	if m.proc != nil {
		st.Owned = true
		st.PID = m.proc.PID()
	}
	st.UserTabReady = m.tab != nil && !m.tab.isClosed()
	return st
}

func (m *Manager) closeTabLocked() {
	if m.tab != nil {
		m.tab.Close()
		m.tab = nil
	}
}

// UserTab returns the dedicated user tab, creating it if needed.
func (m *Manager) UserTab(ctx context.Context) (*UserTab, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tab != nil && !m.tab.isClosed() {
		return m.tab, nil
	}
	m.closeTabLocked()
	bc, err := m.browserClient(ctx)
	if err != nil {
		return nil, err
	}
	defer bc.Close()
	raw, err := bc.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, err
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.TargetID == "" {
		return nil, errors.New("create user tab: no target id")
	}
	tab, err := newUserTab(ctx, m.cfg.Port, res.TargetID, m.logger)
	if err != nil {
		return nil, err
	}
	m.tab = tab
	return tab, nil
}

// Stop closes Chrome gracefully via CDP Browser.close. SIGTERM is never used;
// SIGKILL is a last resort after StopTimeout (only possible when we launched
// the process).
func (m *Manager) Stop(ctx context.Context) (Status, StopResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ver, err := m.probe(ctx)
	if err != nil {
		m.closeTabLocked()
		m.proc = nil
		return m.statusLocked(ctx), StopResult{Graceful: true}, nil
	}
	if m.proc == nil {
		// Not launched by us; only close it if it is our profile.
		if err := m.verifyProfile(ctx, ver); err != nil {
			return Status{}, StopResult{}, err
		}
	}

	// Close the user tab first so it is not restored on the next start.
	if m.tab != nil {
		if bc, err := dialCDP(ctx, ver.WebSocketDebuggerURL, nil); err == nil {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			bc.Call(cctx, "Target.closeTarget", map[string]any{"targetId": m.tab.TargetID()})
			cancel()
			bc.Close()
		}
	}
	m.closeTabLocked()

	proc := m.proc
	if bc, err := dialCDP(ctx, ver.WebSocketDebuggerURL, nil); err == nil {
		// The connection usually drops while the reply is in flight; ignore.
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		bc.Call(cctx, "Browser.close", nil)
		cancel()
		bc.Close()
	} else {
		m.logger.Warn("cdp connect for close failed", "error", err)
	}

	graceful := true
	if !m.waitStopped(ctx, proc, m.cfg.StopTimeout) {
		if proc == nil {
			return m.statusLocked(ctx), StopResult{}, errors.New("chrome did not stop within timeout and was not launched by this process; not killing")
		}
		m.logger.Warn("chrome did not stop gracefully; sending SIGKILL")
		graceful = false
		proc.Kill()
		m.waitStopped(ctx, proc, 5*time.Second)
	}
	m.proc = nil
	return m.statusLocked(ctx), StopResult{Graceful: graceful}, nil
}

func (m *Manager) waitStopped(ctx context.Context, proc Process, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var done <-chan struct{}
	if proc != nil {
		done = proc.Done()
	}
	for {
		if _, err := m.probe(ctx); err != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-done:
			return true
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Shutdown gracefully stops Chrome on agmux server shutdown. Chrome is closed
// (not left running) so cookies are flushed deterministically; it is restored
// by --restore-last-session on the next start. If it is not running, no-op.
func (m *Manager) Shutdown(ctx context.Context) {
	if _, err := m.probe(ctx); err != nil {
		return
	}
	if _, _, err := m.Stop(ctx); err != nil {
		m.logger.Warn("shutdown stop failed", "error", err)
	}
}

type execProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *execProcess) PID() int              { return p.cmd.Process.Pid }
func (p *execProcess) Kill() error           { return p.cmd.Process.Kill() }
func (p *execProcess) Done() <-chan struct{} { return p.done }

func execLauncher(path string, args []string) (Process, error) {
	cmd := exec.Command(path, args...)
	// New session: terminal signals aimed at agmux must not reach Chrome.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &execProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(p.done)
	}()
	return p, nil
}
