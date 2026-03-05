package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// StreamProcess manages a claude CLI process running in stream-json mode.
type StreamProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	lines     []string
	mu        sync.RWMutex
	done      chan struct{}
	file      *os.File
	sessionID string // claude session ID (may differ from agmux session ID after resume)
}

// StartStreamProcess starts a claude CLI subprocess in stream-json mode.
// If resume is true, it uses --resume to continue an existing conversation;
// otherwise it uses --session-id to start a new one.
func StartStreamProcess(sessionID, projectPath string, resume bool) (*StreamProcess, error) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return nil, fmt.Errorf("get streams dir: %w", err)
	}

	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	f, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open stream file: %w", err)
	}

	sessionFlag := "--session-id"
	if resume {
		sessionFlag = "--resume"
	}
	args := []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		sessionFlag, sessionID,
		"--dangerously-skip-permissions",
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = projectPath
	// Filter out CLAUDECODE env var to avoid nested session detection
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("start claude process: %w", err)
	}

	sp := &StreamProcess{
		cmd:   cmd,
		stdin: stdinPipe,
		done:  make(chan struct{}),
		file:  f,
	}

	// Read existing lines from file for continuity
	sp.loadExistingLines(streamPath)

	// Start stdout reader goroutine
	go sp.readLoop(stdoutPipe)

	return sp, nil
}

func (sp *StreamProcess) loadExistingLines(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		sp.lines = append(sp.lines, scanner.Text())
	}
}

func (sp *StreamProcess) readLoop(stdout io.Reader) {
	defer close(sp.done)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Capture session_id from init message
		var msg struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &msg) == nil && msg.Type == "system" && msg.SessionID != "" {
			sp.mu.Lock()
			sp.sessionID = msg.SessionID
			sp.mu.Unlock()
		}

		sp.mu.Lock()
		sp.lines = append(sp.lines, line)
		sp.file.WriteString(line + "\n")
		sp.file.Sync()
		sp.mu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("stream process reader error: %v", err)
	}
}

// SessionID returns the claude session ID assigned by the process.
func (sp *StreamProcess) SessionID() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.sessionID
}

// Send writes a user message to the process stdin in stream-json format.
func (sp *StreamProcess) Send(message string) error {
	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{
		Type: "user",
	}
	msg.Message.Role = "user"
	msg.Message.Content = message

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	// Record user message to memory buffer and JSONL file
	line := string(data)
	sp.mu.Lock()
	sp.lines = append(sp.lines, line)
	sp.file.WriteString(line + "\n")
	sp.file.Sync()
	sp.mu.Unlock()

	_, err = fmt.Fprintf(sp.stdin, "%s\n", data)
	return err
}

// GetLines returns the last N lines from the accumulated output.
func (sp *StreamProcess) GetLines(limit int) []string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	if limit <= 0 || limit >= len(sp.lines) {
		result := make([]string, len(sp.lines))
		copy(result, sp.lines)
		return result
	}

	start := len(sp.lines) - limit
	result := make([]string, limit)
	copy(result, sp.lines[start:])
	return result
}

// Stop gracefully stops the stream process.
func (sp *StreamProcess) Stop() {
	// Close stdin to signal EOF to the process
	sp.stdin.Close()

	// Wait for process to exit with timeout
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- sp.cmd.Wait()
	}()

	select {
	case <-waitDone:
		// Process exited gracefully
	case <-time.After(5 * time.Second):
		// Force kill
		log.Printf("stream process did not exit gracefully, killing")
		sp.cmd.Process.Kill()
		<-waitDone
	}

	sp.file.Close()
}

// Done returns a channel that is closed when the process exits.
func (sp *StreamProcess) Done() <-chan struct{} {
	return sp.done
}
