package session

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestHolder builds a holder that has no CLI process, only what
// readStdout needs: a stream file, a client set and the done channel.
func newTestHolder(t *testing.T) (*holder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &holder{
		sessionID:  "test-session",
		streamFile: f,
		done:       make(chan struct{}),
		clients:    make(map[net.Conn]struct{}),
	}, path
}

func addTestClient(h *holder) <-chan string {
	server, client := net.Pipe()
	h.clientsMu.Lock()
	h.clients[server] = struct{}{}
	h.clientsMu.Unlock()
	out := make(chan string, 100)
	go func() {
		r := bufio.NewReaderSize(client, 1<<20)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				close(out)
				return
			}
			out <- strings.TrimSuffix(line, "\n")
		}
	}()
	return out
}

func bigToolResultLine(toolUseID string, size int) string {
	head := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"` + toolUseID + `","type":"tool_result","content":"`
	tail := `"}]},"parent_tool_use_id":"toolu_parent","session_id":"cli-sess","uuid":"u1"}`
	return head + strings.Repeat("A", size-len(head)-len(tail)) + tail
}

// An oversized stdout line must not stop the reader: later lines are still
// written to the stream file and broadcast, the file keeps one line per
// original line, and h.done only closes at EOF.
func TestHolderReadStdoutSurvivesOversizedLine(t *testing.T) {
	h, path := newTestHolder(t)
	client := addTestClient(h)

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	go h.readStdout(pr)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer pw.Close()
		w := bufio.NewWriter(pw)
		w.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"before"}]}}` + "\n")
		w.WriteString(bigToolResultLine("toolu_big", 17*1000*1000) + "\n")
		w.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"after"}]}}` + "\n")
		w.WriteString(`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"cli-sess"}` + "\n")
		w.Flush()
	}()

	// The writer (standing in for the CLI) must not block on a stalled reader.
	select {
	case <-writerDone:
	case <-time.After(20 * time.Second):
		t.Fatal("CLI writer blocked: holder stopped draining stdout after the oversized line")
	}

	var got []string
	timeout := time.After(10 * time.Second)
	for len(got) < 4 {
		select {
		case l, ok := <-client:
			if !ok {
				t.Fatalf("client closed early, got %d lines", len(got))
			}
			got = append(got, l)
		case <-timeout:
			t.Fatalf("timed out waiting for broadcast lines, got %d: %v", len(got), got)
		}
	}

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("h.done not closed after stdout EOF")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileLines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(fileLines) != 4 {
		t.Fatalf("stream file has %d lines, want 4 (one per original line)", len(fileLines))
	}
	for i, l := range fileLines {
		if !json.Valid([]byte(l)) {
			t.Errorf("stream line %d is not valid JSON: %.100s", i, l)
		}
		if l != got[i] {
			t.Errorf("broadcast line %d differs from file line", i)
		}
	}
	if !strings.Contains(fileLines[2], "after") || !strings.Contains(fileLines[3], `"result"`) {
		t.Errorf("lines after the oversized one were not delivered: %.200s | %.200s", fileLines[2], fileLines[3])
	}
	if len(fileLines[1]) > 4096 {
		t.Errorf("replacement line is too large: %d bytes", len(fileLines[1]))
	}
}

// h.done must stay open while the CLI is still running after an oversized line.
func TestHolderDoneNotClosedEarlyByOversizedLine(t *testing.T) {
	h, _ := newTestHolder(t)
	pr, pw := io.Pipe()
	go h.readStdout(pr)

	go pw.Write([]byte(bigToolResultLine("toolu_x", 11*1024*1024) + "\n"))
	select {
	case <-h.done:
		t.Fatal("h.done closed after an oversized line while stdout is still open")
	case <-time.After(1500 * time.Millisecond):
	}
	pw.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("h.done not closed at EOF")
	}
}

// An oversized INPUT line (user message) is not forwarded, does not kill the
// connection, and the sender is told via a control error + a stream notice.
func TestHolderHandleClientOversizedInput(t *testing.T) {
	h, path := newTestHolder(t)
	stdinR, stdinW := io.Pipe()
	h.stdin = stdinW

	srvConn, cliConn := net.Pipe()
	h.clientsMu.Lock()
	h.clients[srvConn] = struct{}{}
	h.clientsMu.Unlock()
	go h.handleClient(srvConn)

	// what the client (server process) receives from the holder
	recv := make(chan string, 10)
	go func() {
		r := bufio.NewReader(cliConn)
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			recv <- strings.TrimSuffix(l, "\n")
		}
	}()
	// what the CLI receives on stdin
	stdinLines := make(chan string, 10)
	go func() {
		r := bufio.NewReaderSize(stdinR, 1<<20)
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			stdinLines <- strings.TrimSuffix(l, "\n")
		}
	}()

	go func() {
		w := bufio.NewWriter(cliConn)
		w.WriteString(`{"type":"user","message":"first"}` + "\n")
		w.WriteString(`{"type":"user","message":"` + strings.Repeat("B", 11*1024*1024) + `"}` + "\n")
		w.WriteString(`{"type":"user","message":"second"}` + "\n")
		w.Flush()
	}()

	var forwarded []string
	for len(forwarded) < 2 {
		select {
		case l := <-stdinLines:
			forwarded = append(forwarded, l)
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out; forwarded so far: %v", forwarded)
		}
	}
	if !strings.Contains(forwarded[0], "first") || !strings.Contains(forwarded[1], "second") {
		t.Fatalf("unexpected forwarded lines: %.100v", forwarded)
	}
	for _, l := range forwarded {
		if len(l) > 1000 {
			t.Fatalf("oversized input was forwarded to the CLI")
		}
	}

	// sender is notified through the control error event, and the stream has a notice
	var sawControlErr bool
	deadline := time.After(5 * time.Second)
	for !sawControlErr {
		select {
		case l := <-recv:
			var m HolderControlMessage
			if json.Unmarshal([]byte(l), &m) == nil && m.Type == "control" && m.Event == "error" && strings.Contains(m.Content, "not delivered") {
				sawControlErr = true
			}
		case <-deadline:
			t.Fatal("no control error event for the oversized input")
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"oversized_input"`) {
		t.Errorf("stream file has no oversized_input notice: %s", data)
	}
}
