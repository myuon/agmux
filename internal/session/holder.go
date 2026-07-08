package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// SocketDir returns the directory for holder Unix sockets.
// It uses os.TempDir() which respects the $TMPDIR environment variable,
// avoiding issues with macOS cleaning /private/tmp for long-running processes.
func SocketDir() string {
	return filepath.Join(os.TempDir(), "agmux", "socks")
}

// SocketPath returns the Unix socket path for a session.
func SocketPath(sessionID string) string {
	return filepath.Join(SocketDir(), sessionID+".sock")
}

// HolderControlMessage is a message sent over the socket for control purposes.
type HolderControlMessage struct {
	Type    string `json:"type"`
	Action  string `json:"action,omitempty"`  // server → holder: "stop", "restart"
	Event   string `json:"event,omitempty"`   // holder → server: "exited", "hello"
	Code    int    `json:"code,omitempty"`    // exit code
	Content string `json:"content,omitempty"` // message content
	PID     int    `json:"pid,omitempty"`     // holder → server: holder's own PID (for hello)
}

// RunHolder is the main entry point for the holder subprocess.
// It starts the CLI process, listens on a Unix socket, and manages the lifecycle.
func RunHolder(sessionID string, cmdArgs []string, projectPath string, env []string) error {
	if err := os.MkdirAll(SocketDir(), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := SocketPath(sessionID)
	// Clean up stale socket file if it exists
	os.Remove(sockPath)

	// Open JSONL stream file for writing
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return fmt.Errorf("get streams dir: %w", err)
	}
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	streamFile, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open stream file: %w", err)
	}
	defer streamFile.Close()

	// Start the CLI process
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = projectPath
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cli process: %w", err)
	}

	slog.Info("holder: CLI process started", "pid", cmd.Process.Pid, "sessionId", sessionID)

	// Set up Unix socket listener
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("listen unix socket: %w", err)
	}
	defer listener.Close()

	slog.Info("holder: listening on socket", "path", sockPath)

	h := &holder{
		sessionID:  sessionID,
		cmd:        cmd,
		stdin:      stdinPipe,
		streamFile: streamFile,
		listener:   listener,
		done:       make(chan struct{}),
		clients:    make(map[net.Conn]struct{}),
	}

	// Read stdout in a goroutine
	go h.readStdout(stdoutPipe)

	// Accept socket connections in a goroutine
	go h.acceptLoop()

	// Handle SIGTERM gracefully: close stdin so the CLI process exits cleanly,
	// then wait for the done channel.
	//
	// ⚠️ CRITICAL: このハンドラは SIGTERM でのみ発動する。SIGKILL では発動しない。
	// Manager.Delete() から holder を終了させる際は必ず SIGTERM を使うこと。
	// SIGKILL を使うと stdin.Close() が実行されず、claude CLI が孤児化する。
	// See: https://github.com/myuon/agmux/issues/569
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		select {
		case _, ok := <-sigCh:
			if ok {
				slog.Info("holder: received SIGTERM, closing stdin for graceful shutdown", "sessionId", sessionID)
				h.stdin.Close()
			}
		case <-h.done:
			// CLI process already exited; goroutine can exit cleanly.
		}
	}()
	defer signal.Stop(sigCh)

	// Wait for CLI process to exit
	<-h.done
	exitCode := 0
	var exitContent string
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			exitContent = fmt.Sprintf("process exited with code %d", exitCode)
		} else {
			exitContent = err.Error()
		}
	}
	// If there was a reader error, include it in the exit message
	h.readerErrMu.Lock()
	readerErr := h.readerErr
	h.readerErrMu.Unlock()
	if readerErr != "" {
		if exitContent != "" {
			exitContent = exitContent + "; " + readerErr
		} else {
			exitContent = readerErr
		}
	}
	slog.Info("holder: CLI process exited", "code", exitCode, "content", exitContent, "sessionId", sessionID)
	h.broadcastControl(HolderControlMessage{
		Type:    "control",
		Event:   "exited",
		Code:    exitCode,
		Content: exitContent,
	})

	// Give clients a moment to receive the exit notification
	time.Sleep(100 * time.Millisecond)

	return nil
}

type holder struct {
	sessionID  string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	streamFile *os.File
	listener   net.Listener
	done       chan struct{} // closed when stdout EOF (CLI exited)

	clientsMu sync.Mutex
	clients   map[net.Conn]struct{}

	// readerErr stores any error from readStdout so it can be included in the exit message
	readerErrMu sync.Mutex
	readerErr   string

	// restartInfo holds info needed for Codex restart
	restartMu   sync.Mutex
	cmdArgs     []string
	projectPath string
	env         []string
}

// readStdout reads lines from the CLI process stdout, writes to JSONL file,
// and broadcasts to all connected socket clients.
func (h *holder) readStdout(stdout io.Reader) {
	defer close(h.done)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Write to JSONL file (permanent record)
		h.streamFile.WriteString(line + "\n")
		h.streamFile.Sync()

		// Broadcast to all connected socket clients
		h.broadcastLine(line)
	}

	if err := scanner.Err(); err != nil {
		errMsg := fmt.Sprintf("stdout reader error: %v", err)
		slog.Error("holder: "+errMsg, "sessionId", h.sessionID)
		// Store error for inclusion in exit message
		h.readerErrMu.Lock()
		h.readerErr = errMsg
		h.readerErrMu.Unlock()
		// Broadcast error to connected clients so the server can update session status
		h.broadcastControl(HolderControlMessage{
			Type:    "control",
			Event:   "error",
			Content: errMsg,
		})
	}
}

// broadcastLine sends a line to all connected socket clients.
func (h *holder) broadcastLine(line string) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	for conn := range h.clients {
		// Write line + newline; ignore errors (client may have disconnected)
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(conn, "%s\n", line); err != nil {
			slog.Debug("holder: failed to write to client, removing", "error", err)
			conn.Close()
			delete(h.clients, conn)
		}
	}
}

// broadcastControl sends a control message to all connected socket clients.
func (h *holder) broadcastControl(msg HolderControlMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.broadcastLine(string(data))
}

// acceptLoop accepts incoming Unix socket connections.
func (h *holder) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			// Listener closed
			return
		}

		h.clientsMu.Lock()
		h.clients[conn] = struct{}{}
		h.clientsMu.Unlock()

		slog.Info("holder: client connected", "sessionId", h.sessionID)

		// Immediately send a hello so the client can verify it connected to
		// the expected holder (i.e. not to a previous holder that was about to
		// exit on the same socket path). The PID lets the client detect the
		// race window where the old listener is still alive while the new
		// one is being spawned with the same socket path.
		hello, err := json.Marshal(HolderControlMessage{
			Type:  "control",
			Event: "hello",
			PID:   os.Getpid(),
		})
		if err == nil {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, werr := fmt.Fprintf(conn, "%s\n", hello); werr != nil {
				slog.Debug("holder: failed to write hello to client, removing", "error", werr, "sessionId", h.sessionID)
				h.clientsMu.Lock()
				delete(h.clients, conn)
				h.clientsMu.Unlock()
				conn.Close()
				continue
			}
			// Reset write deadline so subsequent broadcasts use their own deadlines.
			conn.SetWriteDeadline(time.Time{})
		}

		// Handle incoming messages from this client
		go h.handleClient(conn)
	}
}

// handleClient reads messages from a socket client and processes them.
func (h *holder) handleClient(conn net.Conn) {
	defer func() {
		h.clientsMu.Lock()
		delete(h.clients, conn)
		h.clientsMu.Unlock()
		conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Check if this is a control message
		var msg HolderControlMessage
		if json.Unmarshal([]byte(line), &msg) == nil && msg.Type == "control" {
			h.handleControl(msg)
			continue
		}

		// Otherwise, forward to CLI stdin
		if _, err := fmt.Fprintf(h.stdin, "%s\n", line); err != nil {
			slog.Error("holder: failed to write to stdin", "error", err, "sessionId", h.sessionID)
			return
		}
	}
}

// handleControl processes control messages from the server.
func (h *holder) handleControl(msg HolderControlMessage) {
	switch msg.Action {
	case "stop":
		slog.Info("holder: received stop command", "sessionId", h.sessionID)
		h.stdin.Close()
	case "restart":
		slog.Info("holder: received restart command", "sessionId", h.sessionID)
		// For Codex: close stdin to let the current process exit,
		// then the holder will exit and the server should spawn a new holder.
		h.stdin.Close()
	default:
		slog.Warn("holder: unknown control action", "action", msg.Action, "sessionId", h.sessionID)
	}
}

// SpawnHolder starts a new holder process that is detached from the current process group.
// It returns the PID of the holder process.
func SpawnHolder(sessionID string, cmdArgs []string, projectPath string, env []string) (int, error) {
	if err := os.MkdirAll(SocketDir(), 0700); err != nil {
		return 0, fmt.Errorf("create socket dir: %w", err)
	}

	// Find the agmux binary path
	agmuxBin, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("get executable path: %w", err)
	}

	// Build holder command arguments.
	// --agmux-dir is an identification marker so that a reaper of one agmux
	// instance does not kill holders belonging to another instance (e.g. an
	// isolated dev server running with a different HOME). See ReapOrphanHolders.
	holderArgs := []string{"holder", "--session-id", sessionID, "--project-path", projectPath}
	if agmuxDir, err := db.AgmuxDir(); err == nil {
		holderArgs = append(holderArgs, "--agmux-dir", agmuxDir)
	}
	holderArgs = append(holderArgs, "--")
	holderArgs = append(holderArgs, cmdArgs...)

	cmd := exec.Command(agmuxBin, holderArgs...)
	cmd.Env = env
	cmd.Dir = projectPath

	// Detach from parent process group using SysProcAttr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Redirect holder's stdout/stderr to a log file for debugging
	holderLogDir := filepath.Join(SocketDir(), "..", "logs")
	os.MkdirAll(holderLogDir, 0700)
	logPath := filepath.Join(holderLogDir, sessionID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("open holder log: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start holder: %w", err)
	}

	pid := cmd.Process.Pid
	slog.Info("holder spawned", "pid", pid, "sessionId", sessionID)

	// Release the process so it's truly detached
	cmd.Process.Release()
	logFile.Close()

	return pid, nil
}

// ConnectToHolder connects to a holder's Unix socket and returns the connection.
// It retries for a short period to allow the holder time to start listening.
//
// Deprecated: prefer ConnectToHolderExpectingPID when the expected holder PID
// is known. Plain ConnectToHolder can race with a previous holder whose
// listener is still alive on the same socket path during its shutdown window
// (see #656). Returned conn does NOT consume the leading hello control message,
// so the caller's readLoop will see it as the first line on the socket.
func ConnectToHolder(sessionID string) (net.Conn, error) {
	sockPath := SocketPath(sessionID)

	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			return conn, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("connect to holder socket %s: %w", sockPath, err)
}

// ConnectToHolderExpectingPID dials the holder's socket and reads the leading
// hello control message to confirm we connected to the holder whose PID is
// expectedPID. If the dial lands on a different (e.g. previous) holder, the
// connection is closed and the dial is retried. This eliminates the race
// described in #656 where the daemon connected to a dying old holder during
// the small shutdown window where its listener was still bound to the shared
// socket path.
//
// If expectedPID is 0, the function accepts the first holder that responds
// with a hello (no PID match required).
//
// Backward compatibility: if a connection succeeds but no hello arrives within
// the read deadline AND expectedPID == 0 (recover path), the connection is
// returned as-is. This lets the daemon recover connections to long-lived
// holders started by an older agmux binary that does not speak hello.
//
// When expectedPID > 0 (spawn/restart path), a hello timeout is treated as a
// hard error: the holder we just spawned must be from the current binary and
// therefore must send hello. Falling back silently in that case would re-open
// the very race window #656 fixes (we could end up trusting a dying old
// listener that happens to share the socket path).
//
// The hello message, when present, is consumed from the connection: callers
// should NOT see it in their subsequent reads.
func ConnectToHolderExpectingPID(sessionID string, expectedPID int) (net.Conn, error) {
	sockPath := SocketPath(sessionID)

	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	mismatchAttempts := 0
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Read the hello message byte-by-byte until newline so we do NOT
		// over-read into the caller's subsequent stream (which they will
		// read via their own bufio.Reader on the same conn). Use a short
		// read deadline so a stuck or dead-but-still-bound socket doesn't
		// block us forever.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, rerr := readLineFromConn(conn, 4096)
		_ = conn.SetReadDeadline(time.Time{})

		if rerr != nil {
			// Read timeout or EOF before hello. Two cases:
			// 1) Pre-hello holder (older binary): no hello will ever come.
			//    Best-effort: return the conn so the daemon can still receive
			//    broadcast events. Only safe when expectedPID == 0 (i.e. the
			//    recover path where we don't know which binary spawned the
			//    holder). For new spawn / restart paths we know the holder
			//    binary is current and must speak hello — silently falling
			//    back there would mask the very race #656 is trying to fix.
			// 2) Newly spawned holder that hasn't accepted yet, or dying old
			//    holder whose conn is broken. Retry.
			// We discriminate by errno: io.EOF or "connection reset" means
			// the conn is dead; timeout means the holder is silent (likely
			// pre-hello).
			if isReadTimeout(rerr) {
				if expectedPID == 0 {
					slog.Warn("holder: no hello received within deadline; assuming pre-hello holder",
						"sessionId", sessionID, "expected_pid", expectedPID)
					return conn, nil
				}
				// expectedPID > 0: the holder we want is from the current
				// binary, which must send hello. A timeout here is a real
				// failure (likely the accept goroutine of the new holder is
				// briefly stuck, or we somehow landed on a dying old holder
				// that no longer responds). Fail fast instead of silently
				// regressing into the pre-fix race window.
				conn.Close()
				lastErr = fmt.Errorf("hello timeout from holder (expected pid %d)", expectedPID)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			conn.Close()
			lastErr = fmt.Errorf("read hello: %w", rerr)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var msg HolderControlMessage
		if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &msg); err != nil || msg.Type != "control" || msg.Event != "hello" {
			// First line wasn't a hello — could be a stale broadcast from a
			// pre-hello holder. Same logic as the timeout branch: only treat
			// this as a pre-hello fallback on the recover path (expectedPID
			// == 0). On spawn/restart, force a retry so we don't trust a
			// connection that doesn't start with a verifiable hello.
			if expectedPID == 0 {
				slog.Warn("holder: first line was not hello; assuming pre-hello holder",
					"sessionId", sessionID, "line", line)
				return conn, nil
			}
			conn.Close()
			lastErr = fmt.Errorf("hello malformed from holder (expected pid %d): %q", expectedPID, line)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if expectedPID > 0 && msg.PID != expectedPID {
			// We dialed an old (or unexpected) holder. Close and retry —
			// the old one is shutting down on the same socket path; subsequent
			// dials will eventually land on the new listener.
			mismatchAttempts++
			slog.Info("holder: hello PID mismatch, retrying", "sessionId", sessionID, "expected", expectedPID, "got", msg.PID, "attempt", mismatchAttempts)
			conn.Close()
			lastErr = fmt.Errorf("hello pid %d != expected %d", msg.PID, expectedPID)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return nil, fmt.Errorf("connect to holder socket %s expecting pid %d: %w", sockPath, expectedPID, lastErr)
}

// isReadTimeout reports whether err is a net.Conn read deadline / timeout.
func isReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}

// readLineFromConn reads bytes from conn one at a time until it hits '\n' or
// the line exceeds maxBytes. Used during the hello handshake to avoid the
// over-read problem that a bufio.Reader would cause (any bytes the bufio
// reader pulled in past the hello newline would be invisible to the caller's
// own bufio.Reader on the same conn).
func readLineFromConn(conn net.Conn, maxBytes int) (string, error) {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	for len(buf) < maxBytes {
		n, err := conn.Read(one)
		if err != nil {
			return string(buf), err
		}
		if n == 0 {
			continue
		}
		if one[0] == '\n' {
			return string(buf), nil
		}
		buf = append(buf, one[0])
	}
	return string(buf), fmt.Errorf("line exceeded %d bytes", maxBytes)
}

// IsHolderAlive checks if a holder process with the given PID is still running.
func IsHolderAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; send signal 0 to check existence
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
