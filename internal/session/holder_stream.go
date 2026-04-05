package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// HolderStreamProcess manages a CLI process through a holder subprocess.
// Instead of directly holding pipes to the CLI process, it communicates
// through a Unix socket to the holder.
type HolderStreamProcess struct {
	conn      net.Conn
	lines     []string
	mu        sync.RWMutex
	done      chan struct{} // closed when the holder's CLI process has exited
	sessionID string       // CLI session ID
	provider  Provider
	holderPID int

	// Fields for Codex followup support
	streamOpts StreamOpts

	// stopped is true when Stop() was called explicitly
	stopped bool

	// holderError stores the last error reported by the holder via "error" control message
	holderError string

	// modelCaptured is true once the model has been captured
	modelCaptured bool

	// Callbacks
	onSessionID      func(cliSessionID string)
	onModel          func(model string)
	onNewLines       func(sessionID string, newLines []string, total int)
	onProcessExit    func(sessionID string, exitErr error)
	onTurnComplete   func(sessionID string)
	onHolderRestart  func(sessionID string, newPID int)
	runningTasks     int
}

// StartHolderStreamProcess starts a CLI process via a holder subprocess.
func StartHolderStreamProcess(opts StreamOpts, provider Provider) (*HolderStreamProcess, error) {
	// Build the command that the holder will execute
	cmd := provider.BuildStreamCommand(opts)
	cmd.Env = provider.AppendOTelEnv(cmd.Env, 0)

	cmdArgs := append([]string{cmd.Path}, cmd.Args[1:]...)

	// Spawn the holder process
	pid, err := SpawnHolder(opts.SessionID, cmdArgs, opts.ProjectPath, cmd.Env)
	if err != nil {
		return nil, fmt.Errorf("spawn holder: %w", err)
	}

	// Connect to the holder's socket
	conn, err := ConnectToHolder(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("connect to holder: %w", err)
	}

	sp := &HolderStreamProcess{
		conn:       conn,
		done:       make(chan struct{}),
		sessionID:  opts.CLISessionID,
		provider:   provider,
		holderPID:  pid,
		streamOpts: opts,
	}

	// Load existing lines from JSONL file (respecting clear offset)
	sp.loadExistingLines(opts.SessionID, opts.ClearOffset)

	// Start reading from the socket
	go sp.readLoop()

	return sp, nil
}

// ReconnectHolderStreamProcess reconnects to an existing holder process.
func ReconnectHolderStreamProcess(opts StreamOpts, provider Provider, holderPID int) (*HolderStreamProcess, error) {
	if !IsHolderAlive(holderPID) {
		return nil, fmt.Errorf("holder process %d is not alive", holderPID)
	}

	conn, err := ConnectToHolder(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("reconnect to holder: %w", err)
	}

	sp := &HolderStreamProcess{
		conn:       conn,
		done:       make(chan struct{}),
		sessionID:  opts.CLISessionID,
		provider:   provider,
		holderPID:  holderPID,
		streamOpts: opts,
	}

	// Load all existing lines from JSONL file (respecting clear offset)
	sp.loadExistingLines(opts.SessionID, opts.ClearOffset)

	// Start reading from the socket
	go sp.readLoop()

	return sp, nil
}

func (sp *HolderStreamProcess) loadExistingLines(sessionID string, clearOffset int64) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return
	}
	path := filepath.Join(streamsDir, sessionID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Seek past the clear offset so only post-clear content is loaded
	if clearOffset > 0 {
		if _, err := f.Seek(clearOffset, io.SeekStart); err != nil {
			return
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Normalize lines from JSONL (holder writes raw lines)
		normalized := sp.provider.NormalizeStreamLine([]byte(line))
		if normalized == nil {
			continue
		}
		normalizedStr := string(normalized)

		// Skip stream_events (transient, not for history)
		if len(normalized) > 0 && normalized[0] == '{' {
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(normalized, &peek) == nil && peek.Type == "stream_event" {
				continue
			}
		}

		sp.lines = append(sp.lines, normalizedStr)
	}
}

func (sp *HolderStreamProcess) readLoop() {
	defer close(sp.done)

	reader := bufio.NewReader(sp.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				slog.Error("holder stream: read error", "error", err)
			}
			return
		}
		// Trim the trailing newline
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if line == "" {
			continue
		}

		// Check if this is a control message from the holder
		var ctrlMsg HolderControlMessage
		if json.Unmarshal([]byte(line), &ctrlMsg) == nil && ctrlMsg.Type == "control" {
			if ctrlMsg.Event == "error" {
				slog.Warn("holder stream: holder reported error", "content", ctrlMsg.Content, "sessionId", sp.streamOpts.SessionID)
				// Store the error so it can be included when the process exits
				sp.mu.Lock()
				sp.holderError = ctrlMsg.Content
				sp.mu.Unlock()
				continue
			}
			if ctrlMsg.Event == "exited" {
				slog.Info("holder stream: CLI process exited", "code", ctrlMsg.Code, "content", ctrlMsg.Content, "sessionId", sp.streamOpts.SessionID)
				sp.mu.RLock()
				stopped := sp.stopped
				cb := sp.onProcessExit
				holderErr := sp.holderError
				sp.mu.RUnlock()

				if !stopped && cb != nil {
					var exitErr error
					// Build error from exit code, content, and any previously reported holder error
					errMsg := ctrlMsg.Content
					if errMsg == "" && holderErr != "" {
						errMsg = holderErr
					}
					if ctrlMsg.Code != 0 {
						if errMsg != "" {
							exitErr = fmt.Errorf("exit code %d: %s", ctrlMsg.Code, errMsg)
						} else {
							exitErr = fmt.Errorf("exit code %d", ctrlMsg.Code)
						}
					} else if errMsg != "" {
						exitErr = fmt.Errorf("%s", errMsg)
					}
					cb(sp.streamOpts.SessionID, exitErr)
				}
				return
			}
			continue
		}

		// Capture session_id via provider
		if sid, ok := sp.provider.ParseSessionID([]byte(line)); ok {
			sp.mu.Lock()
			sp.sessionID = sid
			cb := sp.onSessionID
			sp.mu.Unlock()
			if cb != nil {
				cb(sid)
			}
		}

		// Capture model name via provider (only once)
		if !sp.modelCaptured {
			if model, ok := sp.provider.ParseModel([]byte(line)); ok {
				sp.mu.Lock()
				sp.modelCaptured = true
				cb := sp.onModel
				sp.mu.Unlock()
				if cb != nil {
					cb(model)
				}
			}
		}

		// Track background tasks and detect turn completion.
		ev := parseStreamEvent([]byte(line))
		switch ev.Type {
		case "task_started":
			sp.mu.Lock()
			sp.runningTasks++
			sp.mu.Unlock()
		case "task_notification":
			sp.mu.Lock()
			if sp.runningTasks > 0 {
				sp.runningTasks--
			}
			sp.mu.Unlock()
		case "result":
			if ev.Subtype == "success" {
				sp.mu.RLock()
				idle := sp.runningTasks == 0
				tcb := sp.onTurnComplete
				sp.mu.RUnlock()
				if idle && tcb != nil {
					tcb(sp.streamOpts.SessionID)
				}
			}
		case "turn.completed":
			// Codex emits turn.completed when a turn finishes.
			sp.mu.RLock()
			tcb := sp.onTurnComplete
			sp.mu.RUnlock()
			if tcb != nil {
				tcb(sp.streamOpts.SessionID)
			}
		}

		// Normalize the line
		normalized := sp.provider.NormalizeStreamLine([]byte(line))
		if normalized == nil {
			continue
		}
		normalizedStr := string(normalized)

		// Check for stream_event (real-time only, not persisted)
		isStreamEvent := false
		if len(normalized) > 0 && normalized[0] == '{' {
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(normalized, &peek) == nil && peek.Type == "stream_event" {
				isStreamEvent = true
			}
		}

		sp.mu.Lock()
		if !isStreamEvent {
			sp.lines = append(sp.lines, normalizedStr)
			// Note: holder already writes to JSONL file, so we don't write here
		}
		total := len(sp.lines)
		cb := sp.onNewLines
		sp.mu.Unlock()

		if cb != nil {
			cb(sp.streamOpts.SessionID, []string{normalizedStr}, total)
		}
	}
}

// SessionID returns the CLI session ID.
func (sp *HolderStreamProcess) SessionID() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.sessionID
}

// ProviderName returns the name of the provider used by this process.
func (sp *HolderStreamProcess) ProviderName() ProviderName {
	return sp.provider.Name()
}

// HolderPID returns the PID of the holder process.
func (sp *HolderStreamProcess) HolderPID() int {
	return sp.holderPID
}

// SetOnSessionID sets a callback for CLI session ID capture.
func (sp *HolderStreamProcess) SetOnSessionID(fn func(cliSessionID string)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onSessionID = fn
}

// SetOnModel sets a callback for model name capture.
func (sp *HolderStreamProcess) SetOnModel(fn func(model string)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onModel = fn
}

// SetOnNewLines sets a callback for new stream lines.
func (sp *HolderStreamProcess) SetOnNewLines(fn func(sessionID string, newLines []string, total int)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onNewLines = fn
}

// SetOnProcessExit sets a callback for process exit.
func (sp *HolderStreamProcess) SetOnProcessExit(fn func(sessionID string, exitErr error)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onProcessExit = fn
}

// SetOnTurnComplete sets a callback for when the CLI completes a turn.
func (sp *HolderStreamProcess) SetOnTurnComplete(fn func(sessionID string)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onTurnComplete = fn
}

func (sp *HolderStreamProcess) SetOnHolderRestart(fn func(sessionID string, newPID int)) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.onHolderRestart = fn
}

// Send writes a user message to the holder's socket.
func (sp *HolderStreamProcess) Send(message string) error {
	return sp.SendWithImages(message, nil)
}

// SendWithImages writes a user message with optional images via the socket.
func (sp *HolderStreamProcess) SendWithImages(message string, images []ImageData) error {
	// For Codex, check if the process has finished and handle restart
	if sp.provider.Name() == ProviderCodex {
		return sp.sendCodex(message)
	}

	return sp.sendClaude(message, images)
}

func (sp *HolderStreamProcess) sendClaude(message string, images []ImageData) error {
	var data []byte
	var err error

	if len(images) > 0 {
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

	// Record user message in memory (holder writes to JSONL file via stdin echo)
	line := string(data)
	sp.mu.Lock()
	sp.lines = append(sp.lines, line)
	total := len(sp.lines)

	// Also write to JSONL file from server side for user messages
	// (holder only writes stdout from CLI, not stdin messages)
	sp.writeToStreamFile(line)

	cb := sp.onNewLines
	sp.mu.Unlock()

	if cb != nil {
		cb(sp.streamOpts.SessionID, []string{line}, total)
	}

	// Send to holder via socket
	_, err = fmt.Fprintf(sp.conn, "%s\n", data)
	return err
}

func (sp *HolderStreamProcess) writeToStreamFile(line string) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return
	}
	path := filepath.Join(streamsDir, sp.streamOpts.SessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n")
	f.Sync()
}

// sendCodex handles message sending for Codex provider via holder.
func (sp *HolderStreamProcess) sendCodex(message string) error {
	// Record the user message
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
	total := len(sp.lines)
	sp.writeToStreamFile(line)
	cb := sp.onNewLines
	sp.mu.Unlock()

	if cb != nil {
		cb(sp.streamOpts.SessionID, []string{line}, total)
	}

	// For Codex, send restart control to holder (which will close stdin,
	// wait for process exit, then the holder exits).
	// After holder exits, we need to spawn a new holder with resume args.
	sp.mu.Lock()
	sp.stopped = true
	sp.mu.Unlock()

	// Send stop to current holder
	ctrlMsg, _ := json.Marshal(HolderControlMessage{
		Type:   "control",
		Action: "stop",
	})
	fmt.Fprintf(sp.conn, "%s\n", ctrlMsg)

	// Wait for holder to exit
	select {
	case <-sp.done:
	case <-time.After(10 * time.Minute):
		return fmt.Errorf("timeout waiting for codex holder to exit")
	}

	// Get CLI session ID for resume
	sp.mu.RLock()
	cliSessionID := sp.sessionID
	sp.mu.RUnlock()

	if cliSessionID == "" {
		return fmt.Errorf("no CLI session ID available for codex resume")
	}

	slog.Info("restarting codex via new holder", "cliSessionID", cliSessionID, "message", message)
	return sp.restartForCodex(message, cliSessionID)
}

// restartForCodex spawns a new holder for a Codex followup message.
func (sp *HolderStreamProcess) restartForCodex(message, cliSessionID string) error {
	opts := sp.streamOpts
	opts.Resume = true
	opts.CLISessionID = cliSessionID
	opts.InitialPrompt = message

	cmd := sp.provider.BuildStreamCommand(opts)
	cmd.Env = sp.provider.AppendOTelEnv(cmd.Env, 0)
	cmdArgs := append([]string{cmd.Path}, cmd.Args[1:]...)

	pid, err := SpawnHolder(opts.SessionID, cmdArgs, opts.ProjectPath, cmd.Env)
	if err != nil {
		return fmt.Errorf("spawn holder for codex resume: %w", err)
	}

	conn, err := ConnectToHolder(opts.SessionID)
	if err != nil {
		return fmt.Errorf("connect to holder for codex resume: %w", err)
	}

	sp.mu.Lock()
	sp.conn = conn
	sp.holderPID = pid
	sp.done = make(chan struct{})
	sp.stopped = false
	onRestart := sp.onHolderRestart
	sp.mu.Unlock()

	go sp.readLoop()

	if onRestart != nil {
		onRestart(opts.SessionID, pid)
	}

	return nil
}

// recordUserMessage records a user message without sending it to stdin.
func (sp *HolderStreamProcess) recordUserMessage(message string) {
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
	total := len(sp.lines)
	sp.writeToStreamFile(line)
	cb := sp.onNewLines
	sp.mu.Unlock()

	if cb != nil {
		cb(sp.streamOpts.SessionID, []string{line}, total)
	}
}

// GetLines returns the last N lines.
func (sp *HolderStreamProcess) GetLines(limit int) []string {
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

// GetLinesAfter returns lines added after the given index.
func (sp *HolderStreamProcess) GetLinesAfter(after int) ([]string, int) {
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

// TotalLines returns the total number of lines.
func (sp *HolderStreamProcess) TotalLines() int {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return len(sp.lines)
}

// ClearLines clears the in-memory lines buffer.
func (sp *HolderStreamProcess) ClearLines() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.lines = nil
}

// Stop sends a stop command to the holder.
func (sp *HolderStreamProcess) Stop() {
	sp.mu.Lock()
	sp.stopped = true
	sp.mu.Unlock()

	// Send stop control message
	ctrlMsg, _ := json.Marshal(HolderControlMessage{
		Type:   "control",
		Action: "stop",
	})
	fmt.Fprintf(sp.conn, "%s\n", ctrlMsg)

	// Wait for holder to exit
	select {
	case <-sp.done:
	case <-time.After(5 * time.Second):
		// Force kill the holder process
		slog.Warn("holder did not exit gracefully, killing", "pid", sp.holderPID)
		if p, err := os.FindProcess(sp.holderPID); err == nil {
			p.Kill()
		}
		<-sp.done
	}

	sp.conn.Close()
}

// Done returns a channel that is closed when the holder's CLI process exits.
func (sp *HolderStreamProcess) Done() <-chan struct{} {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.done
}

// IsExited returns true if the holder's CLI process has exited.
func (sp *HolderStreamProcess) IsExited() bool {
	sp.mu.RLock()
	ch := sp.done
	sp.mu.RUnlock()
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
