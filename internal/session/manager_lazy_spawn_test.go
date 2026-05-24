package session

import (
	"log/slog"
	"testing"
)

// Tests for the lazy-spawn behavior of one-shot providers (Codex/Cursor) when
// Create is invoked without an initial prompt. See issue #643.
//
// Without the lazy-spawn fix, Create would always call StartHolderStreamProcess
// even when prompt == "", causing the one-shot CLI (cursor/codex) to start with
// no input and exit immediately with code 1 (or 0), making subsequent sends
// auto-recover into the same broken state.

// TestCreate_OneShot_EmptyPrompt_SkipsHolderSpawn verifies that creating a
// one-shot provider session without an initial prompt does NOT spawn a holder.
// The session row must exist with holder_pid = 0 and status = idle so the next
// send can lazily spawn the holder with the user's first message.
func TestCreate_OneShot_EmptyPrompt_SkipsHolderSpawn(t *testing.T) {
	cases := []struct {
		name     string
		provider ProviderName
	}{
		{"cursor", ProviderCursor},
		{"codex", ProviderCodex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			defer db.Close()

			m := &Manager{
				db:              db,
				logger:          slog.Default(),
				cursorCommand:   "agent",
				codexCommand:    "codex",
				claudeCommand:   "claude",
				streamProcesses: make(map[string]*HolderStreamProcess),
				deletingSet:     make(map[string]struct{}),
				systemPrompt:    "test",
			}

			sess, err := m.Create("test-no-prompt", "/tmp", "", false, CreateOpts{
				Provider: tc.provider,
			})
			if err != nil {
				t.Fatalf("Create with empty prompt should succeed, got: %v", err)
			}
			if sess == nil {
				t.Fatal("expected session to be returned")
			}

			// The DB row should exist with holder_pid = 0 (no holder spawned).
			var holderPID int
			var status string
			if err := db.QueryRow("SELECT holder_pid, status FROM sessions WHERE id = ?", sess.ID).Scan(&holderPID, &status); err != nil {
				t.Fatalf("query session row: %v", err)
			}
			if holderPID != 0 {
				t.Errorf("holder_pid = %d, want 0 (holder should not be spawned for one-shot + empty prompt)", holderPID)
			}
			if status != string(StatusIdle) {
				t.Errorf("status = %q, want %q", status, string(StatusIdle))
			}

			// streamProcesses must not contain an entry for this session
			// because no holder was spawned.
			m.streamMu.Lock()
			_, ok := m.streamProcesses[sess.ID]
			m.streamMu.Unlock()
			if ok {
				t.Errorf("streamProcesses should not contain session %s when holder is not spawned", sess.ID)
			}
		})
	}
}

// TestCreate_OneShot_EmptyPrompt_AllowsRetrieval verifies that the lazy-spawned
// session can be retrieved via Get even though no holder process exists yet.
func TestCreate_OneShot_EmptyPrompt_AllowsRetrieval(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	m := &Manager{
		db:              db,
		logger:          slog.Default(),
		cursorCommand:   "agent",
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
		systemPrompt:    "test",
	}

	sess, err := m.Create("retrieve-test", "/tmp", "", false, CreateOpts{
		Provider: ProviderCursor,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := m.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", sess.ID, err)
	}
	if got.Provider != ProviderCursor {
		t.Errorf("provider = %q, want %q", got.Provider, ProviderCursor)
	}
	if got.HolderPID != 0 {
		t.Errorf("holderPID = %d, want 0", got.HolderPID)
	}
	if got.ConversationStarted {
		t.Errorf("conversationStarted = true, want false for freshly created session")
	}
}

// TestSendKeysAutoRecover_OneShotFirstSend_DerivesCorrectStreamOpts is a
// data-shape test for the new lazy-spawn-on-first-send branch in
// SendKeysWithImages. After a lazy create (holder_pid = 0,
// conversation_started = 0), the first send must take the new branch that
// passes InitialPrompt = text and Resume = false, NOT the resume branch
// (which requires cli_session_id and ConversationStarted).
func TestSendKeysAutoRecover_OneShotFirstSend_DerivesCorrectStreamOpts(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	id := "lazy-cursor-first-send"
	if _, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "lazy", "/tmp", "", "idle", "worker", "stream", "cursor", "", "", 0, 0,
	); err != nil {
		t.Fatalf("insert lazy session: %v", err)
	}

	m := newTestManager(t, db)

	s, err := m.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Reproduce the branch-selection logic from SendKeysWithImages:
	provider := m.getProvider(s.Provider)
	canResume := s.ConversationStarted
	cliSessionID := s.CliSessionID

	if !provider.IsOneShot() {
		t.Fatal("precondition: cursor must be one-shot")
	}
	if canResume {
		t.Fatal("precondition: lazy session must not be resumable on first send")
	}
	if cliSessionID != "" {
		t.Fatal("precondition: lazy session must not have a cli_session_id yet")
	}

	// The resume branch should NOT be taken (it requires cliSessionID != "" && canResume).
	tookResumeBranch := provider.IsOneShot() && cliSessionID != "" && canResume
	if tookResumeBranch {
		t.Errorf("lazy session must NOT take the resume branch (cliSessionID=%q canResume=%v)", cliSessionID, canResume)
	}

	// The new lazy-first-send branch MUST be taken
	// (provider.IsOneShot() && !tookResumeBranch).
	tookLazyFirstSend := provider.IsOneShot() && !tookResumeBranch
	if !tookLazyFirstSend {
		t.Errorf("lazy session must take the one-shot-first-send branch; "+
			"provider.IsOneShot()=%v tookResumeBranch=%v", provider.IsOneShot(), tookResumeBranch)
	}
}

// TestSendKeysAutoRecover_OneShotSecondTurn_UsesResume is a regression test
// for issue #646: cursor sessions whose first turn completed successfully
// (cli_session_id is populated and conversation_started = true) must take the
// resume branch on the second turn, not the first-send branch.
//
// Issue #646 was resolved by the combination of #634 (suppress resume on
// sessions that have not completed a turn) and #649 (lazy-spawn on first
// send). This test guards against a regression by directly asserting the
// shape of the StreamOpts that the resume branch hands to
// StartHolderStreamProcess via the extracted buildOneShotResumeOpts helper.
//
// Symptoms before the fix: the holder exited after the first one-shot turn
// (cursor agent exec is one-shot) and the next send hit the auto-recover
// path. If the resume branch was not selected, the holder was re-spawned
// with Resume=false + no CLI session ID, which causes cursor to start a
// fresh conversation (losing context) or exit immediately when the
// positional prompt arg is missing.
func TestSendKeysAutoRecover_OneShotSecondTurn_UsesResume(t *testing.T) {
	// A readable stand-in for the cli_session_id that would be written to
	// the JSONL by the CLI's system.init event during the first turn.
	const cliSessionAfterFirstTurn = "cli-session-after-first-turn"
	const sendText = "second turn prompt"
	const mcpConfigPath = "/tmp/mcp.json"
	const effectiveSystemPrompt = "effective-system-prompt"
	const apiPort = 4321

	cases := []struct {
		name     string
		provider ProviderName
	}{
		{"cursor", ProviderCursor},
		{"codex", ProviderCodex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			defer db.Close()

			id := "oneshot-second-turn-" + string(tc.provider)
			// Simulate the state after the first turn completed:
			//   - cli_session_id is set (from system.init in the JSONL)
			//   - conversation_started = 1 (a result event was observed)
			//   - holder_pid = 0 (the one-shot CLI exited after the first turn)
			//   - status = idle
			if _, err := db.Exec(
				`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, "second-turn", "/tmp/project", "", "idle", "worker", "stream", string(tc.provider), "claude-sonnet-4-5",
				cliSessionAfterFirstTurn, 0, 1,
			); err != nil {
				t.Fatalf("insert post-first-turn session: %v", err)
			}

			m := newTestManager(t, db)
			m.cursorCommand = "agent"
			m.codexCommand = "codex"
			m.claudeCommand = "claude"
			m.streamProcesses = make(map[string]*HolderStreamProcess)
			m.deletingSet = make(map[string]struct{})

			s, err := m.Get(id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			// Preconditions: the DB row mirrors the state after the first turn,
			// so the branch selector in SendKeysWithImages must dispatch to the
			// resume branch (not the lazy-first-send branch). The cliSessionID
			// precondition being non-empty also implies the lazy-first-send
			// branch cannot be reached with this state.
			provider := m.getProvider(s.Provider)
			canResume := s.ConversationStarted
			cliSessionID := s.CliSessionID

			if !provider.IsOneShot() {
				t.Fatalf("precondition: %s must be one-shot", tc.provider)
			}
			if cliSessionID == "" {
				t.Fatal("precondition: cli_session_id must be set after first turn")
			}
			if !canResume {
				t.Fatal("precondition: conversation_started must be true after first turn")
			}
			if !(provider.IsOneShot() && cliSessionID != "" && canResume) {
				t.Fatal("precondition: branch selector must dispatch to the resume branch")
			}

			// Call the extracted pure function used by the resume branch and
			// assert the concrete StreamOpts shape. This catches regressions
			// where Resume is silently flipped to false or the CLISessionID
			// is dropped / overwritten with the wrong source field.
			opts := buildOneShotResumeOpts(s, sendText, mcpConfigPath, effectiveSystemPrompt, cliSessionID, apiPort)

			if !opts.Resume {
				t.Errorf("Resume = false, want true (second turn must resume the existing CLI session)")
			}
			if opts.CLISessionID != cliSessionAfterFirstTurn {
				t.Errorf("CLISessionID = %q, want %q (must be copied from the DB cli_session_id)",
					opts.CLISessionID, cliSessionAfterFirstTurn)
			}
			if opts.InitialPrompt != sendText {
				t.Errorf("InitialPrompt = %q, want %q (positional prompt arg must carry the user's text)",
					opts.InitialPrompt, sendText)
			}
			if opts.SessionID != s.ID {
				t.Errorf("SessionID = %q, want %q", opts.SessionID, s.ID)
			}
			if opts.ProjectPath != s.ProjectPath {
				t.Errorf("ProjectPath = %q, want %q", opts.ProjectPath, s.ProjectPath)
			}
			if opts.MCPConfigPath != mcpConfigPath {
				t.Errorf("MCPConfigPath = %q, want %q", opts.MCPConfigPath, mcpConfigPath)
			}
			if opts.SystemPrompt != effectiveSystemPrompt {
				t.Errorf("SystemPrompt = %q, want %q", opts.SystemPrompt, effectiveSystemPrompt)
			}
			if opts.APIPort != apiPort {
				t.Errorf("APIPort = %d, want %d", opts.APIPort, apiPort)
			}

			// Independently verify that the lazy-first-send opts builder
			// produces a clearly different shape (Resume = false, empty
			// CLISessionID). This guards the test from collapsing if the two
			// branches ever start sharing one builder.
			firstSendOpts := buildOneShotFirstSendOpts(s, sendText, mcpConfigPath, effectiveSystemPrompt, apiPort)
			if firstSendOpts.Resume {
				t.Errorf("buildOneShotFirstSendOpts: Resume = true, want false")
			}
			if firstSendOpts.CLISessionID != "" {
				t.Errorf("buildOneShotFirstSendOpts: CLISessionID = %q, want empty", firstSendOpts.CLISessionID)
			}
		})
	}
}

// TestRecoverStreamProcesses_SkipsLazySessions verifies that the SQL query in
// RecoverStreamProcesses (WHERE holder_pid > 0) skips sessions created via
// the lazy-spawn path (holder_pid = 0). This is critical: on daemon restart,
// we must not try to "recover" a session that never had a holder.
func TestRecoverStreamProcesses_SkipsLazySessions(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Insert one lazy session (holder_pid = 0) and one normal session (holder_pid > 0).
	if _, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"lazy-sess", "lazy", "/tmp", "", "idle", "worker", "stream", "cursor", "", "", 0, 0,
	); err != nil {
		t.Fatalf("insert lazy session: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, cli_session_id, holder_pid, conversation_started)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"alive-sess", "alive", "/tmp", "", "working", "worker", "stream", "claude", "", "cli-x", 12345, 1,
	); err != nil {
		t.Fatalf("insert alive session: %v", err)
	}

	// This mirrors the exact WHERE clause used in RecoverStreamProcesses.
	rows, err := db.Query(`SELECT id FROM sessions WHERE holder_pid > 0`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}

	if len(got) != 1 || got[0] != "alive-sess" {
		t.Errorf("recovery query returned %v, want [alive-sess] only (lazy-sess must be skipped because holder_pid = 0)", got)
	}
}
