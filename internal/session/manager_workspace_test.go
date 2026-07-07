package session

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestCreate_EmptyProjectPath_UsesWorkspaceDir verifies that when Create is
// called with an empty projectPath, the session's ProjectPath is set to
// ~/.agmux/workspaces/<sessionID> and the directory is created on disk.
//
// We use a Codex provider with an empty prompt so that no holder process is
// spawned (see manager_lazy_spawn_test.go), allowing the test to run without
// any external CLIs.
func TestCreate_EmptyProjectPath_UsesWorkspaceDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	testDB := newTestDB(t)
	defer testDB.Close()

	m := &Manager{
		db:              testDB,
		logger:          slog.Default(),
		codexCommand:    "codex",
		claudeCommand:   "claude",
		cursorCommand:   "agent",
		streamProcesses: make(map[string]*HolderStreamProcess),
		deletingSet:     make(map[string]struct{}),
		systemPrompt:    "test",
	}

	sess, err := m.Create("workspace-test", "", "", false, CreateOpts{
		Provider: ProviderCodex,
	})
	if err != nil {
		t.Fatalf("Create with empty projectPath should succeed, got: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session to be returned")
	}

	// ProjectPath should be under <tmpHome>/.agmux/workspaces/<sessionID>
	wantPath := filepath.Join(tmpHome, ".agmux", "workspaces", sess.ID)
	if sess.ProjectPath != wantPath {
		t.Errorf("ProjectPath = %q, want %q", sess.ProjectPath, wantPath)
	}

	// The directory must have been created on disk
	info, err := os.Stat(sess.ProjectPath)
	if err != nil {
		t.Fatalf("workspace dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("workspace path %q should be a directory", sess.ProjectPath)
	}

	// The DB row should also reflect the resolved projectPath
	var storedPath string
	if err := testDB.QueryRow("SELECT project_path FROM sessions WHERE id = ?", sess.ID).Scan(&storedPath); err != nil {
		t.Fatalf("query project_path: %v", err)
	}
	if storedPath != sess.ProjectPath {
		t.Errorf("stored project_path %q != sess.ProjectPath %q", storedPath, sess.ProjectPath)
	}
}
