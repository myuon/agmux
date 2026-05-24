package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// newTestHomeConfig points config.Load() and collectGlobalSkills() at a fresh
// temp HOME and writes an optional ~/.agmux/config.toml with the given body.
// The previous HOME is restored automatically via t.Cleanup.
func newTestHomeConfig(t *testing.T, configBody string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if configBody != "" {
		agmuxDir := filepath.Join(home, ".agmux")
		if err := os.MkdirAll(agmuxDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agmuxDir, "config.toml"), []byte(configBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestGetHostInfo(t *testing.T) {
	// Isolate HOME so provider config / skills come from a clean dir.
	newTestHomeConfig(t, "")

	s := &Server{startTime: time.Now().Add(-90 * time.Second)}

	req := httptest.NewRequest(http.MethodGet, "/api/host-info", nil)
	rec := httptest.NewRecorder()

	s.getHostInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Decode into a generic map to assert the expected top-level keys exist.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	for _, key := range []string{"machine", "providers", "skills"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response missing key %q; body = %s", key, rec.Body.String())
		}
	}

	// Decode into the typed struct and sanity-check the machine fields.
	var resp hostInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode hostInfoResponse: %v", err)
	}
	if resp.Machine.OS != runtime.GOOS {
		t.Errorf("machine.os = %q, want %q", resp.Machine.OS, runtime.GOOS)
	}
	if resp.Machine.Arch != runtime.GOARCH {
		t.Errorf("machine.arch = %q, want %q", resp.Machine.Arch, runtime.GOARCH)
	}
	if resp.Machine.PID != os.Getpid() {
		t.Errorf("machine.pid = %d, want %d", resp.Machine.PID, os.Getpid())
	}
	if resp.Machine.Uptime == "" {
		t.Error("machine.uptime is empty")
	}
	// MemoryBytes is the daemon (Go runtime) memory usage; it must be non-zero
	// for a running process.
	if resp.Machine.MemoryBytes == 0 {
		t.Error("machine.memoryBytes is zero, want daemon memory > 0")
	}
	if len(resp.Providers) == 0 {
		t.Error("providers is empty, want claude/codex/cursor entries")
	}
	// Skills must be a non-nil slice (so it serializes as [] not null).
	if resp.Skills == nil {
		t.Error("skills is nil, want a (possibly empty) slice")
	}
}

func TestCollectProviderInfo(t *testing.T) {
	// Create a temp dir with a fake executable that LookPath can resolve, and
	// prepend it to PATH so availability is deterministic.
	binDir := t.TempDir()
	fakeBin := "agmux-fake-provider"
	fakePath := filepath.Join(binDir, fakeBin)
	if err := os.WriteFile(fakePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Point claude at the fake (available) binary and the others at a name that
	// cannot resolve (unavailable).
	missing := "agmux-definitely-missing-binary"
	configBody := "" +
		"[session]\n" +
		"claude_command = \"" + fakeBin + "\"\n" +
		"codex_command = \"" + missing + "\"\n" +
		"cursor_command = \"" + missing + "\"\n"
	newTestHomeConfig(t, configBody)

	s := &Server{startTime: time.Now()}
	providers := s.collectProviderInfo()

	byName := make(map[string]hostProviderInfo, len(providers))
	for _, p := range providers {
		byName[p.Name] = p
	}

	claude, ok := byName["claude"]
	if !ok {
		t.Fatalf("missing claude provider in %+v", providers)
	}
	if claude.Command != fakeBin {
		t.Errorf("claude command = %q, want %q", claude.Command, fakeBin)
	}
	if !claude.Available {
		t.Errorf("claude should be available (resolves to %s)", fakePath)
	}

	codex, ok := byName["codex"]
	if !ok {
		t.Fatalf("missing codex provider in %+v", providers)
	}
	if codex.Available {
		t.Errorf("codex should be unavailable (command %q does not exist)", codex.Command)
	}
}

func TestCollectProviderInfoCommandWithFlags(t *testing.T) {
	// A configured command may include flags (e.g. "claude --foo --bar"). The
	// availability check must resolve only the leading executable token, not the
	// whole string, otherwise LookPath fails on the flags.
	binDir := t.TempDir()
	fakeBin := "agmux-fake-provider"
	fakePath := filepath.Join(binDir, fakeBin)
	if err := os.WriteFile(fakePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	claudeCmd := fakeBin + " --foo --bar"
	configBody := "" +
		"[session]\n" +
		"claude_command = \"" + claudeCmd + "\"\n"
	newTestHomeConfig(t, configBody)

	s := &Server{startTime: time.Now()}
	providers := s.collectProviderInfo()

	byName := make(map[string]hostProviderInfo, len(providers))
	for _, p := range providers {
		byName[p.Name] = p
	}

	claude, ok := byName["claude"]
	if !ok {
		t.Fatalf("missing claude provider in %+v", providers)
	}
	if !claude.Available {
		t.Errorf("claude should be available when command has flags (resolves to %s)", fakePath)
	}
	// The displayed command keeps the full configured string (flags included).
	if claude.Command != claudeCmd {
		t.Errorf("claude command = %q, want %q", claude.Command, claudeCmd)
	}
}

func TestCollectGlobalSkills(t *testing.T) {
	home := newTestHomeConfig(t, "")
	skillsDir := filepath.Join(home, ".claude", "skills")

	// valid skill: directory containing SKILL.md
	mustMkdir(t, filepath.Join(skillsDir, "alpha"))
	mustWrite(t, filepath.Join(skillsDir, "alpha", "SKILL.md"), "# alpha")

	mustMkdir(t, filepath.Join(skillsDir, "beta"))
	mustWrite(t, filepath.Join(skillsDir, "beta", "SKILL.md"), "# beta")

	// directory without SKILL.md should be ignored
	mustMkdir(t, filepath.Join(skillsDir, "no-skill-md"))
	mustWrite(t, filepath.Join(skillsDir, "no-skill-md", "README.md"), "# nope")

	// a plain file at the skills root should be ignored
	mustWrite(t, filepath.Join(skillsDir, "loose-file.md"), "# loose")

	skills := collectGlobalSkills()

	got := make(map[string]bool, len(skills))
	for _, s := range skills {
		got[s] = true
	}

	if !got["alpha"] || !got["beta"] {
		t.Errorf("expected alpha and beta in skills, got %v", skills)
	}
	if got["no-skill-md"] {
		t.Errorf("directory without SKILL.md should be excluded, got %v", skills)
	}
	if got["loose-file.md"] {
		t.Errorf("loose file should be excluded, got %v", skills)
	}
	if len(skills) != 2 {
		t.Errorf("expected exactly 2 skills, got %d (%v)", len(skills), skills)
	}
}

func TestCollectGlobalSkillsMissingDir(t *testing.T) {
	// HOME has no ~/.claude/skills at all; should return an empty (non-nil) slice.
	newTestHomeConfig(t, "")

	skills := collectGlobalSkills()
	if skills == nil {
		t.Fatal("skills is nil, want empty slice")
	}
	if len(skills) != 0 {
		t.Errorf("expected no skills, got %v", skills)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
