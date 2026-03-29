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
	Event   string `json:"event,omitempty"`   // holder → server: "exited"
	Code    int    `json:"code,omitempty"`     // exit code
	Content string `json:"content,omitempty"` // message content
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
	defer func() {
		listener.Close()
		// NOTE: We intentionally do NOT remove the socket file here.
		// If a new holder has already been spawned for the same session,
		// removing the file would delete the new holder's socket, causing
		// the new holder's connections to break. The new holder cleans up
		// the stale socket file itself before creating its listener.
	}()

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

	// Ignore SIGTERM so that holder survives server restarts (launchctl kickstart -k).
	// Holder shutdown is controlled via socket "stop" command, not OS signals.
	signal.Ignore(syscall.SIGTERM)

	// Wait for CLI process to exit
	<-h.done
	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	slog.Info("holder: CLI process exited", "code", exitCode, "sessionId", sessionID)
	h.broadcastControl(HolderControlMessage{
		Type:  "control",
		Event: "exited",
		Code:  exitCode,
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
		slog.Error("holder: stdout reader error", "error", err, "sessionId", h.sessionID)
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

	// Build holder command arguments
	holderArgs := []string{"holder", "--session-id", sessionID, "--project-path", projectPath, "--"}
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
