package session

import (
	"database/sql"
	"strings"
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
// to the database when the conversation has not started yet (DB update only).
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

// TestSwitchModel_WorkingReturnsError verifies that SwitchModel refuses to
// switch while a turn is in progress (status = working) and leaves the DB
// model unchanged.
func TestSwitchModel_WorkingReturnsError(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "switch-working"
	insertSwitchModelSession(t, db, id, "claude", "working", "claude-sonnet-4-5", 1)

	m := newTestManager(t, db)
	err := m.SwitchModel(id, "claude-opus-4-6")
	if err == nil {
		t.Fatal("SwitchModel should return an error while the session is working")
	}
	if !strings.Contains(err.Error(), "working") {
		t.Errorf("error = %q, want mention of working state", err.Error())
	}

	if got := querySessionModel(t, db, id); got != "claude-sonnet-4-5" {
		t.Errorf("model = %q, want unchanged %q", got, "claude-sonnet-4-5")
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
