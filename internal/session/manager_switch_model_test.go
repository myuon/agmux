package session

import (
	"bufio"
	"database/sql"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// insertSwitchModelSession inserts a session row for SwitchModel tests.
func insertSwitchModelSession(t *testing.T, db *sql.DB, id, provider, status, model string, conversationStarted int) {
	t.Helper()
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, conversation_started, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", status, "worker", "stream", provider, model, conversationStarted, 0, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func querySessionModel(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var model string
	if err := db.QueryRow("SELECT model FROM sessions WHERE id = ?", id).Scan(&model); err != nil {
		t.Fatalf("query model: %v", err)
	}
	return model
}

// TestSwitchModel_UpdatesDB verifies that SwitchModel persists the new model
// to the database when the conversation has not started yet and no holder is
// running (DB update only).
func TestSwitchModel_UpdatesDB(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "switch-db-only"
	insertSwitchModelSession(t, db, id, "claude", "idle", "claude-sonnet-4-5", 0)

	m := newTestManager(t, db)
	if err := m.SwitchModel(id, "claude-opus-4-6"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}

	if got := querySessionModel(t, db, id); got != "claude-opus-4-6" {
		t.Errorf("model = %q, want %q", got, "claude-opus-4-6")
	}
}

// TestSwitchModel_OneShotUpdatesStreamOpts verifies that for one-shot
// providers (codex/cursor) the in-memory streamOpts.Model is updated so the
// next followup turn re-spawns the CLI with the new model.
func TestSwitchModel_OneShotUpdatesStreamOpts(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "switch-oneshot"
	insertSwitchModelSession(t, db, id, "codex", "idle", "gpt-5", 1)

	m := newTestManager(t, db)
	sp := &HolderStreamProcess{
		streamOpts: StreamOpts{SessionID: id, Model: "gpt-5"},
	}
	m.streamProcesses = map[string]*HolderStreamProcess{id: sp}

	if err := m.SwitchModel(id, "gpt-5-codex"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}

	if got := querySessionModel(t, db, id); got != "gpt-5-codex" {
		t.Errorf("model = %q, want %q", got, "gpt-5-codex")
	}
	if got := sp.Model(); got != "gpt-5-codex" {
		t.Errorf("streamOpts.Model = %q, want %q", got, "gpt-5-codex")
	}
}

// TestSwitchModel_OneShotWithoutStreamProcess verifies that one-shot sessions
// without an in-memory stream process still get the DB update (the next
// auto-recovery spawn reads the model from the DB).
func TestSwitchModel_OneShotWithoutStreamProcess(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "switch-oneshot-no-sp"
	insertSwitchModelSession(t, db, id, "codex", "idle", "gpt-5", 1)

	m := newTestManager(t, db)
	m.streamProcesses = map[string]*HolderStreamProcess{}

	if err := m.SwitchModel(id, "gpt-5-codex"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if got := querySessionModel(t, db, id); got != "gpt-5-codex" {
		t.Errorf("model = %q, want %q", got, "gpt-5-codex")
	}
}

// TestSwitchModel_ResidentNoActiveHolder verifies that a claude session with
// a started conversation but no active holder gets only the DB update (no
// restart attempt, which would fail without a real holder binary).
func TestSwitchModel_ResidentNoActiveHolder(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "switch-claude-no-holder"
	insertSwitchModelSession(t, db, id, "claude", "idle", "claude-sonnet-4-5", 1)

	m := newTestManager(t, db)
	m.streamProcesses = map[string]*HolderStreamProcess{}

	if err := m.SwitchModel(id, "claude-opus-4-6"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if got := querySessionModel(t, db, id); got != "claude-opus-4-6" {
		t.Errorf("model = %q, want %q", got, "claude-opus-4-6")
	}
}

// fakeHolderPID is a PID that is never alive, so killStaleHolder / Stop's
// force-kill fallback never touch a real process.
const fakeHolderPID = 99999999

// fakeHolder is an in-memory stand-in for a holder process: the stream
// process talks to one end of a net.Pipe and the test drives the other end.
type fakeHolder struct {
	sp       *HolderStreamProcess
	server   net.Conn
	received chan string   // non-control lines written by the daemon (user messages)
	stopped  chan struct{} // closed when the daemon sends the stop control message
}

func newFakeHolder(t *testing.T, opts StreamOpts) *fakeHolder {
	t.Helper()
	client, server := net.Pipe()
	fh := &fakeHolder{
		sp: &HolderStreamProcess{
			conn:       client,
			done:       make(chan struct{}),
			provider:   NewClaudeProvider("", ""),
			holderPID:  fakeHolderPID,
			streamOpts: opts,
		},
		server:   server,
		received: make(chan string, 16),
		stopped:  make(chan struct{}),
	}
	go func() {
		r := bufio.NewReader(server)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.Contains(line, `"action":"stop"`) {
				close(fh.stopped)
				// Closing our end makes the daemon's read loop hit EOF and exit.
				_ = server.Close()
				return
			}
			fh.received <- line
		}
	}()
	t.Cleanup(func() { _ = server.Close() })
	return fh
}

// emitTurnComplete makes the fake CLI report a successful turn result.
func (fh *fakeHolder) emitTurnComplete(t *testing.T) {
	t.Helper()
	if _, err := fh.server.Write([]byte(`{"type":"result","subtype":"success"}` + "\n")); err != nil {
		t.Fatalf("write result event: %v", err)
	}
}

// spawnRecorder replaces StartHolderStreamProcess and records each spawn.
type spawnRecorder struct {
	t       *testing.T
	calls   chan StreamOpts
	mu      sync.Mutex
	holders []*fakeHolder
}

func newSpawnRecorder(t *testing.T, m *Manager) *spawnRecorder {
	r := &spawnRecorder{t: t, calls: make(chan StreamOpts, 8)}
	m.startHolderFn = func(opts StreamOpts, provider Provider) (*HolderStreamProcess, error) {
		fh := newFakeHolder(t, opts)
		r.mu.Lock()
		r.holders = append(r.holders, fh)
		r.mu.Unlock()
		r.calls <- opts
		return fh.sp, nil
	}
	return r
}

func (r *spawnRecorder) waitSpawn() StreamOpts {
	r.t.Helper()
	select {
	case opts := <-r.calls:
		return opts
	case <-time.After(5 * time.Second):
		r.t.Fatal("holder was not respawned within 5s")
		return StreamOpts{}
	}
}

func (r *spawnRecorder) assertNoSpawn() {
	r.t.Helper()
	select {
	case opts := <-r.calls:
		r.t.Fatalf("unexpected holder spawn: %+v", opts)
	default:
	}
}

func (r *spawnRecorder) last() *fakeHolder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.holders[len(r.holders)-1]
}

// newSwitchModelTestManager returns a manager backed by a fresh DB with HOME
// pointed at a temp dir (MCP configs / stream files are written there).
func newSwitchModelTestManager(t *testing.T) (*Manager, *sql.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	m := newTestManager(t, db)
	m.streamProcesses = map[string]*HolderStreamProcess{}
	m.deletingSet = map[string]struct{}{}
	return m, db
}

// attachFakeHolder registers a running fake holder for the session, wired the
// same way Create / Restart wire a real one.
func attachFakeHolder(t *testing.T, m *Manager, id string, opts StreamOpts) *fakeHolder {
	t.Helper()
	opts.SessionID = id
	fh := newFakeHolder(t, opts)
	m.wireSessionIDCallback(id, fh.sp)
	fh.sp.StartReadLoop()
	m.streamMu.Lock()
	m.streamProcesses[id] = fh.sp
	m.streamMu.Unlock()
	return fh
}

func queryPendingModel(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var v string
	if err := db.QueryRow("SELECT pending_model FROM sessions WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatalf("query pending_model: %v", err)
	}
	return v
}

func waitStopped(t *testing.T, fh *fakeHolder) {
	t.Helper()
	select {
	case <-fh.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("old holder was not stopped within 5s")
	}
}

// TestSwitchModel_NotStartedRespawnsFresh verifies #714: when the holder was
// already spawned (Create / Duplicate) but no turn has completed, switching
// the model restarts the holder fresh (not --resume) with the new model,
// carries over in-memory-only options, and drops the stale CLI session ID.
func TestSwitchModel_NotStartedRespawnsFresh(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	id := "switch-not-started"
	insertSwitchModelSession(t, db, id, "claude", "idle", "claude-opus-5[1m]", 0)
	if _, err := db.Exec("UPDATE sessions SET cli_session_id = 'stale-uuid' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	old := attachFakeHolder(t, m, id, StreamOpts{Model: "claude-opus-5[1m]", Worktree: true})
	rec := newSpawnRecorder(t, m)

	if err := m.SwitchModel(id, "claude-opus-5-5"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}

	waitStopped(t, old)
	opts := rec.waitSpawn()
	if opts.Resume {
		t.Error("Resume = true, want fresh start (no conversation to resume)")
	}
	if opts.CLISessionID != "" {
		t.Errorf("CLISessionID = %q, want empty", opts.CLISessionID)
	}
	if opts.Model != "claude-opus-5-5" {
		t.Errorf("spawn Model = %q, want %q", opts.Model, "claude-opus-5-5")
	}
	if !opts.Worktree {
		t.Error("Worktree option of the replaced process was not carried over")
	}
	if got := querySessionModel(t, db, id); got != "claude-opus-5-5" {
		t.Errorf("model = %q, want %q", got, "claude-opus-5-5")
	}
	var cliSessionID string
	if err := db.QueryRow("SELECT cli_session_id FROM sessions WHERE id = ?", id).Scan(&cliSessionID); err != nil {
		t.Fatal(err)
	}
	if cliSessionID != "" {
		t.Errorf("cli_session_id = %q, want reset to empty", cliSessionID)
	}
	m.streamMu.Lock()
	cur := m.streamProcesses[id]
	m.streamMu.Unlock()
	if cur != rec.last().sp {
		t.Error("stream process map does not point at the new holder")
	}
}

// TestSwitchModel_WorkingReservesPending verifies #715: while a turn is in
// progress the switch is reserved instead of rejected, a second selection
// overwrites the reservation, and selecting the current model cancels it.
// The running holder must not be touched.
func TestSwitchModel_WorkingReservesPending(t *testing.T) {
	for _, status := range []string{"working", "waiting_input"} {
		t.Run(status, func(t *testing.T) {
			m, db := newSwitchModelTestManager(t)
			id := "switch-reserve"
			insertSwitchModelSession(t, db, id, "claude", status, "claude-sonnet-4-5", 1)
			attachFakeHolder(t, m, id, StreamOpts{Model: "claude-sonnet-4-5"})
			rec := newSpawnRecorder(t, m)

			if err := m.SwitchModel(id, "claude-opus-4-6"); err != nil {
				t.Fatalf("SwitchModel: %v", err)
			}
			if got := queryPendingModel(t, db, id); got != "claude-opus-4-6" {
				t.Errorf("pending_model = %q, want %q", got, "claude-opus-4-6")
			}
			if got := querySessionModel(t, db, id); got != "claude-sonnet-4-5" {
				t.Errorf("model = %q, want unchanged %q", got, "claude-sonnet-4-5")
			}

			// A second selection overwrites the reservation.
			if err := m.SwitchModel(id, "claude-haiku-4-5"); err != nil {
				t.Fatalf("SwitchModel overwrite: %v", err)
			}
			if got := queryPendingModel(t, db, id); got != "claude-haiku-4-5" {
				t.Errorf("pending_model = %q, want overwritten %q", got, "claude-haiku-4-5")
			}
			s, err := m.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if s.PendingModel != "claude-haiku-4-5" {
				t.Errorf("Session.PendingModel = %q, want %q", s.PendingModel, "claude-haiku-4-5")
			}

			// Selecting the current model cancels the reservation.
			if err := m.SwitchModel(id, "claude-sonnet-4-5"); err != nil {
				t.Fatalf("SwitchModel cancel: %v", err)
			}
			if got := queryPendingModel(t, db, id); got != "" {
				t.Errorf("pending_model = %q, want cancelled", got)
			}
			rec.assertNoSpawn()
		})
	}
}

// TestSwitchModel_ReservationAppliedOnTurnComplete verifies #715 (and #714
// for a session whose first turn is running): a model reserved during a turn
// is applied when the turn completes — the holder is restarted with --resume
// and the new model, and the reservation is cleared.
func TestSwitchModel_ReservationAppliedOnTurnComplete(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	id := "switch-apply-on-complete"
	insertSwitchModelSession(t, db, id, "claude", "working", "claude-sonnet-4-5", 0)
	if _, err := db.Exec("UPDATE sessions SET cli_session_id = 'cli-uuid-1' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	old := attachFakeHolder(t, m, id, StreamOpts{Model: "claude-sonnet-4-5"})
	rec := newSpawnRecorder(t, m)

	if err := m.SwitchModel(id, "claude-opus-4-6"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	rec.assertNoSpawn()

	// The first turn finishes: conversation_started becomes 1 and the
	// reservation is applied via --resume.
	old.emitTurnComplete(t)

	opts := rec.waitSpawn()
	waitStopped(t, old)
	if !opts.Resume {
		t.Error("Resume = false, want --resume to keep the conversation")
	}
	if opts.CLISessionID != "cli-uuid-1" {
		t.Errorf("CLISessionID = %q, want %q", opts.CLISessionID, "cli-uuid-1")
	}
	if opts.Model != "claude-opus-4-6" {
		t.Errorf("spawn Model = %q, want %q", opts.Model, "claude-opus-4-6")
	}

	if got := queryPendingModel(t, db, id); got != "" {
		t.Errorf("pending_model = %q, want cleared", got)
	}
	if got := querySessionModel(t, db, id); got != "claude-opus-4-6" {
		t.Errorf("model = %q, want %q", got, "claude-opus-4-6")
	}
	// applyPendingModel re-broadcasts the status after spawning; wait for it
	// to release spawnMu before checking the final status.
	m.spawnMu.Lock()
	m.spawnMu.Unlock() //nolint:staticcheck // barrier
	s, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != StatusIdle {
		t.Errorf("status = %q, want idle", s.Status)
	}
}

// TestSendKeys_AppliesPendingModelBeforeSend verifies the race guard for
// #715: if the next message arrives while a reservation is still pending
// (e.g. before the turn-complete apply got the lock, or after a server
// restart), the switch is applied first and the message goes to the new
// holder — never to the old-model process.
func TestSendKeys_AppliesPendingModelBeforeSend(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	id := "switch-apply-on-send"
	insertSwitchModelSession(t, db, id, "claude", "idle", "claude-sonnet-4-5", 1)
	if _, err := db.Exec("UPDATE sessions SET cli_session_id = 'cli-uuid-2', pending_model = 'claude-opus-4-6' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	old := attachFakeHolder(t, m, id, StreamOpts{Model: "claude-sonnet-4-5"})
	rec := newSpawnRecorder(t, m)

	if err := m.SendKeys(id, "hello next turn"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	opts := rec.waitSpawn()
	waitStopped(t, old)
	if !opts.Resume || opts.Model != "claude-opus-4-6" {
		t.Errorf("spawn opts = resume:%v model:%q, want resume with %q", opts.Resume, opts.Model, "claude-opus-4-6")
	}
	select {
	case line := <-rec.last().received:
		if !strings.Contains(line, "hello next turn") {
			t.Errorf("new holder received %q, want the user message", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message was not delivered to the new holder")
	}
	select {
	case line := <-old.received:
		t.Errorf("old holder received %q, want nothing", line)
	default:
	}
	if got := queryPendingModel(t, db, id); got != "" {
		t.Errorf("pending_model = %q, want cleared", got)
	}
}

// TestSwitchModel_IdleClearsPendingAndApplies verifies that a direct switch
// on an idle session drops any leftover reservation.
func TestSwitchModel_IdleClearsPendingAndApplies(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	id := "switch-idle-clears"
	insertSwitchModelSession(t, db, id, "codex", "idle", "gpt-5", 1)
	if _, err := db.Exec("UPDATE sessions SET pending_model = 'gpt-5-mini' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	sp := &HolderStreamProcess{streamOpts: StreamOpts{SessionID: id, Model: "gpt-5"}}
	m.streamProcesses[id] = sp

	if err := m.SwitchModel(id, "gpt-5-codex"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if got := queryPendingModel(t, db, id); got != "" {
		t.Errorf("pending_model = %q, want cleared", got)
	}
	if got := sp.Model(); got != "gpt-5-codex" {
		t.Errorf("streamOpts.Model = %q, want %q", got, "gpt-5-codex")
	}
}
