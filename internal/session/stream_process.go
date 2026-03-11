package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// StreamProcess manages a CLI process running in stream-json mode.
type StreamProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	lines     []string
	mu        sync.RWMutex
	done      chan struct{}
	file      *os.File
	sessionID string // CLI session ID (may differ from agmux session ID after resume)
	provider  Provider

	// Fields for Codex followup support (process restart on new messages)
	streamOpts StreamOpts // saved opts for rebuilding commands

	// onSessionID is called when the CLI session ID is first captured from stdout.
	// This allows the manager to persist it to the DB.
	onSessionID func(cliSessionID string)
}

// ReadCLISessionID reads the CLI-assigned session ID from a stream JSONL file.
// It delegates parsing to the given provider.
// Returns empty string if no successful session was found.
func ReadCLISessionID(agmuxSessionID string, provider Provider) string {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(streamsDir, agmuxSessionID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lastSessionID string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		if sid, ok := provider.ParseSessionID([]byte(scanner.Text())); ok {
			lastSessionID = sid
		}
	}
	return lastSessionID
}

// StartStreamProcess starts a CLI subprocess in stream-json mode.
// If resume is true, it uses --resume to continue an existing conversation;
// otherwise it uses --session-id to start a new one.
// cliSessionID is only used when resume=true -- it's the CLI-assigned session ID.
// initialPrompt is an optional initial prompt (used by Codex as positional arg).
func StartStreamProcess(sessionID, projectPath, mcpConfigPath, systemPrompt string, resume bool, worktree bool, provider Provider, cliSessionID ...string) (*StreamProcess, error) {
	csid := ""
	if len(cliSessionID) > 0 {
		csid = cliSessionID[0]
	}

	opts := StreamOpts{
		SessionID:     sessionID,
		ProjectPath:   projectPath,
		MCPConfigPath: mcpConfigPath,
		SystemPrompt:  systemPrompt,
		Resume:        resume,
		Worktree:      worktree,
		CLISessionID:  csid,
	}

	return startStreamProcessWithOpts(opts, provider)
}

// StartStreamProcessWithPrompt is like StartStreamProcess but also sets the initial prompt.
// This is used by Codex provider to pass the prompt as a command-line argument.
func StartStreamProcessWithPrompt(sessionID, projectPath, mcpConfigPath, systemPrompt, initialPrompt string, resume bool, worktree bool, provider Provider, cliSessionID ...string) (*StreamProcess, error) {
	csid := ""
	if len(cliSessionID) > 0 {
		csid = cliSessionID[0]
	}

	opts := StreamOpts{
		SessionID:      sessionID,
		ProjectPath:    projectPath,
		MCPConfigPath:  mcpConfigPath,
		SystemPrompt:   systemPrompt,
		InitialPrompt:  initialPrompt,
		Resume:         resume,
		Worktree:       worktree,
		CLISessionID:   csid,
	}

	return startStreamProcessWithOpts(opts, provider)
}

func startStreamProcessWithOpts(opts StreamOpts, provider Provider) (*StreamProcess, error) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return nil, fmt.Errorf("get streams dir: %w", err)
	}

	streamPath := filepath.Join(streamsDir, opts.SessionID+".jsonl")
	f, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open stream file: %w", err)
	}

	cmd := provider.BuildStreamCommand(opts)
	// Inject OTel environment variables for telemetry collection
	cmd.Env = provider.AppendOTelEnv(cmd.Env, 0)

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
		return nil, fmt.Errorf("start cli process: %w", err)
	}

	sp := &StreamProcess{
		cmd:        cmd,
		stdin:      stdinPipe,
		done:       make(chan struct{}),
		file:       f,
		sessionID:  opts.CLISessionID, // Initialize from opts so resume/recovery can use it immediately
		provider:   provider,
		streamOpts: opts,
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

		// Capture session_id via provider (before normalization so
		// provider-specific events like thread.started are still visible).
		if sid, ok := sp.provider.ParseSessionID([]byte(line)); ok {
			sp.mu.Lock()
			sp.sessionID = sid
			cb := sp.onSessionID
			sp.mu.Unlock()
			if cb != nil {
				cb(sid)
			}
		}

		// Normalize the line into Claude-compatible format.
		normalized := sp.provider.NormalizeStreamLine([]byte(line))
		if normalized == nil {
			// Provider says to drop this line (e.g. in-progress events).
			continue
		}
		normalizedStr := string(normalized)

		sp.mu.Lock()
		sp.lines = append(sp.lines, normalizedStr)
		sp.file.WriteString(normalizedStr + "\n")
		sp.file.Sync()
		sp.mu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		slog.Default().Error("stream process reader error", "error", err)
	}
}

// SessionID returns the CLI session ID assigned by the process.
func (sp *StreamProcess) SessionID() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.sessionID
}

// SetOnSessionID sets a callback that fires when the CLI session ID is captured from stdout.
func (sp *StreamProcess) SetOnSessionID(fn func(cliSessionID string)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onSessionID = fn
}

// ImageData represents a base64-encoded image to be sent with a message.
type ImageData struct {
	Data      string `json:"data"`
	MediaType string `json:"mediaType"`
}

// Send writes a user message to the process stdin in stream-json format.
func (sp *StreamProcess) Send(message string) error {
	return sp.SendWithImages(message, nil)
}

// SendWithImages writes a user message with optional images to the process stdin in stream-json format.
// When images are provided, content is sent as an array of content blocks (text + image blocks).
// When no images are provided, content is sent as a plain string for backward compatibility.
//
// For Codex provider: since codex exec exits after processing, followup messages
// require restarting the process with "codex exec resume <session_id> <message>".
func (sp *StreamProcess) SendWithImages(message string, images []ImageData) error {
	// For Codex, check if the process has finished and restart for followup.
	if sp.provider.Name() == ProviderCodex {
		return sp.sendCodex(message)
	}

	return sp.sendClaude(message, images)
}

// sendClaude handles message sending for Claude provider (stdin-based).
func (sp *StreamProcess) sendClaude(message string, images []ImageData) error {
	var data []byte
	var err error

	if len(images) > 0 {
		// Build content array with text block + image blocks
		type imageSource struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		}
		type contentBlock struct {
			Type   string       `json:"type"`
			Text   string       `json:"text,omitempty"`
			Source *imageSource `json:"source,omitempty"`
		}

		var content []contentBlock
		if message != "" {
			content = append(content, contentBlock{Type: "text", Text: message})
		}
		for _, img := range images {
			content = append(content, contentBlock{
				Type: "image",
				Source: &imageSource{
					Type:      "base64",
					MediaType: img.MediaType,
					Data:      img.Data,
				},
			})
		}

		msg := struct {
			Type    string `json:"type"`
			Message struct {
				Role    string         `json:"role"`
				Content []contentBlock `json:"content"`
			} `json:"message"`
		}{
			Type: "user",
		}
		msg.Message.Role = "user"
		msg.Message.Content = content

		data, err = json.Marshal(msg)
	} else {
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

		data, err = json.Marshal(msg)
	}

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

// sendCodex handles message sending for Codex provider.
// Codex exec exits after each prompt, so followup messages require
// spawning a new "codex exec resume <session_id> <message>" process.
func (sp *StreamProcess) sendCodex(message string) error {
	// Record the user message to memory buffer and JSONL file
	userMsg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user"}
	userMsg.Message.Role = "user"
	userMsg.Message.Content = message
	data, err := json.Marshal(userMsg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	line := string(data)
	sp.mu.Lock()
	sp.lines = append(sp.lines, line)
	sp.file.WriteString(line + "\n")
	sp.file.Sync()
	sp.mu.Unlock()

	// Wait for current process to finish (with timeout)
	select {
	case <-sp.done:
		// Process has finished, we can restart
	case <-time.After(10 * time.Minute):
		return fmt.Errorf("timeout waiting for codex process to finish")
	}

	// Get the CLI session ID for resume
	sp.mu.RLock()
	cliSessionID := sp.sessionID
	sp.mu.RUnlock()

	if cliSessionID == "" {
		return fmt.Errorf("no CLI session ID available for codex resume")
	}

	return sp.restartForCodex(message, cliSessionID)
}

// restartForCodex spawns a new codex exec resume process for a followup message.
func (sp *StreamProcess) restartForCodex(message, cliSessionID string) error {
	// Clean up the old process (already exited since done channel was closed)
	sp.stdin.Close()
	_ = sp.cmd.Wait()

	opts := sp.streamOpts
	opts.Resume = true
	opts.CLISessionID = cliSessionID
	opts.InitialPrompt = message

	cmd := sp.provider.BuildStreamCommand(opts)
	cmd.Env = sp.provider.AppendOTelEnv(cmd.Env, 0)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe for codex resume: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe for codex resume: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codex resume process: %w", err)
	}

	sp.mu.Lock()
	sp.cmd = cmd
	sp.stdin = stdinPipe
	sp.done = make(chan struct{})
	sp.mu.Unlock()

	go sp.readLoop(stdoutPipe)

	return nil
}

// recordUserMessage records a user message to the stream file and memory buffer
// without sending it to stdin. Used when the prompt is passed as a command-line arg.
func (sp *StreamProcess) recordUserMessage(message string) {
	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user"}
	msg.Message.Role = "user"
	msg.Message.Content = message
	data, _ := json.Marshal(msg)
	line := string(data)

	sp.mu.Lock()
	sp.lines = append(sp.lines, line)
	sp.file.WriteString(line + "\n")
	sp.file.Sync()
	sp.mu.Unlock()
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

// GetLinesAfter returns lines added after the given index and the current total line count.
func (sp *StreamProcess) GetLinesAfter(after int) ([]string, int) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	total := len(sp.lines)
	if after >= total {
		return nil, total
	}
	if after < 0 {
		after = 0
	}
	result := make([]string, total-after)
	copy(result, sp.lines[after:])
	return result, total
}

// TotalLines returns the current total number of lines.
func (sp *StreamProcess) TotalLines() int {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return len(sp.lines)
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
		slog.Default().Warn("stream process did not exit gracefully, killing")
		sp.cmd.Process.Kill()
		<-waitDone
	}

	sp.file.Close()
}

// Done returns a channel that is closed when the process exits.
func (sp *StreamProcess) Done() <-chan struct{} {
	return sp.done
}
