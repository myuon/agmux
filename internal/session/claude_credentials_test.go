package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// credsJSON builds a Claude Code credential payload expiring at the given time.
func credsJSON(t *testing.T, expiry time.Time) []byte {
	t.Helper()
	payload := map[string]any{
		"claudeAiOauth": map[string]any{
			"expiresAt":        expiry.UnixMilli(),
			"refreshToken":     "refresh",
			"subscriptionType": "max",
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	return b
}

// stubClaudeCreds points the repair logic at in-memory Keychain contents and a
// temp credentials file, restoring the real hooks via t.Cleanup. A nil
// keychain payload means "no entry". It returns a pointer that reports whether
// the delete hook ran.
func stubClaudeCreds(t *testing.T, keychain []byte, file []byte) *bool {
	t.Helper()

	origRead, origDelete, origPath := claudeKeychainRead, claudeKeychainDelete, claudeCredsFilePath
	t.Cleanup(func() {
		claudeKeychainRead, claudeKeychainDelete, claudeCredsFilePath = origRead, origDelete, origPath
	})

	deleted := false
	claudeKeychainRead = func() ([]byte, error) {
		if keychain == nil {
			return nil, errNoClaudeKeychainEntry
		}
		return keychain, nil
	}
	claudeKeychainDelete = func() error {
		deleted = true
		return nil
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if file != nil {
		if err := os.WriteFile(path, file, 0o600); err != nil {
			t.Fatalf("write creds file: %v", err)
		}
	}
	claudeCredsFilePath = func() (string, error) { return path, nil }

	return &deleted
}

// skipUnlessDarwin guards tests whose subject is a no-op on other platforms.
func skipUnlessDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("keychain repair only runs on darwin")
	}
}

func TestRepairClaudeCredentialsRemovesExpiredKeychainEntry(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	// The corruption seen in #701: the Keychain carries expiresAt=0 while the
	// file still holds a usable token.
	deleted := stubClaudeCreds(t, credsJSON(t, time.UnixMilli(0)), credsJSON(t, now.Add(8*time.Hour)))

	state, err := RepairClaudeCredentials(now)
	if err != nil {
		t.Fatalf("RepairClaudeCredentials: %v", err)
	}
	if state != ClaudeCredsRepaired {
		t.Errorf("state = %v, want ClaudeCredsRepaired", state)
	}
	if !*deleted {
		t.Error("expired keychain entry was not deleted")
	}
}

func TestRepairClaudeCredentialsKeepsValidKeychainEntry(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	deleted := stubClaudeCreds(t, credsJSON(t, now.Add(time.Hour)), credsJSON(t, now.Add(8*time.Hour)))

	state, err := RepairClaudeCredentials(now)
	if err != nil {
		t.Fatalf("RepairClaudeCredentials: %v", err)
	}
	if state != ClaudeCredsHealthy {
		t.Errorf("state = %v, want ClaudeCredsHealthy", state)
	}
	if *deleted {
		t.Error("deleted a keychain entry that was still valid")
	}
}

func TestRepairClaudeCredentialsKeepsExpiredEntryWhenFileCannotReplaceIt(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	cases := []struct {
		name string
		file []byte
	}{
		{name: "file missing", file: nil},
		{name: "file also expired", file: credsJSON(t, now.Add(-time.Hour))},
		{name: "file unparseable", file: []byte("not json")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deleted := stubClaudeCreds(t, credsJSON(t, time.UnixMilli(0)), tc.file)

			state, err := RepairClaudeCredentials(now)
			if err != nil {
				t.Fatalf("RepairClaudeCredentials: %v", err)
			}
			if state != ClaudeCredsUnrecoverable {
				t.Errorf("state = %v, want ClaudeCredsUnrecoverable", state)
			}
			// Deleting here would destroy the only credentials left.
			if *deleted {
				t.Error("deleted the keychain entry with no valid fallback")
			}
		})
	}
}

func TestRepairClaudeCredentialsNoKeychainEntry(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	deleted := stubClaudeCreds(t, nil, credsJSON(t, now.Add(8*time.Hour)))

	state, err := RepairClaudeCredentials(now)
	if err != nil {
		t.Fatalf("RepairClaudeCredentials: %v", err)
	}
	if state != ClaudeCredsHealthy {
		t.Errorf("state = %v, want ClaudeCredsHealthy", state)
	}
	if *deleted {
		t.Error("attempted to delete a nonexistent entry")
	}
}

func TestRepairClaudeCredentialsPropagatesDeleteFailure(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	stubClaudeCreds(t, credsJSON(t, time.UnixMilli(0)), credsJSON(t, now.Add(8*time.Hour)))
	claudeKeychainDelete = func() error { return errors.New("boom") }

	state, err := RepairClaudeCredentials(now)
	if err == nil {
		t.Fatal("expected an error when the delete fails")
	}
	if state != ClaudeCredsUnrecoverable {
		t.Errorf("state = %v, want ClaudeCredsUnrecoverable", state)
	}
}

func TestParseClaudeExpiry(t *testing.T) {
	t.Run("zero expiry is a valid, long-past timestamp", func(t *testing.T) {
		got, ok := parseClaudeExpiry([]byte(`{"claudeAiOauth":{"expiresAt":0}}`))
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if !got.Equal(time.UnixMilli(0)) {
			t.Errorf("expiry = %v, want epoch", got)
		}
	})

	t.Run("missing oauth block is not parseable", func(t *testing.T) {
		if _, ok := parseClaudeExpiry([]byte(`{}`)); ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run("invalid json is not parseable", func(t *testing.T) {
		if _, ok := parseClaudeExpiry([]byte(`nope`)); ok {
			t.Error("ok = true, want false")
		}
	})
}

func TestWatchClaudeCredentialsRepairsThenStops(t *testing.T) {
	skipUnlessDarwin(t)
	now := time.Now()

	deleted := stubClaudeCreds(t, credsJSON(t, time.UnixMilli(0)), credsJSON(t, now.Add(8*time.Hour)))

	// A cancelled context still gets one pass, so the daemon repairs
	// immediately at startup rather than waiting out the first interval.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		WatchClaudeCredentials(ctx, time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchClaudeCredentials did not return after ctx cancellation")
	}
	if !*deleted {
		t.Error("watch did not repair the expired keychain entry")
	}
}

// recordingProvider is a Provider that reports a canned repair outcome and
// remembers whether the pre-spawn hook asked for one. It borrows the rest of
// the Provider surface from ClaudeProvider.
type recordingProvider struct {
	*ClaudeProvider
	state  ClaudeCredentialState
	err    error
	called int
}

func (p *recordingProvider) RepairCredentials() (ClaudeCredentialState, error) {
	p.called++
	return p.state, p.err
}

func TestRepairProviderCredentialsInvokesRepairer(t *testing.T) {
	// Every outcome must leave the spawn free to proceed, so the only
	// observable difference between them is what gets logged.
	for _, tc := range []struct {
		name  string
		state ClaudeCredentialState
		err   error
	}{
		{name: "healthy", state: ClaudeCredsHealthy},
		{name: "repaired", state: ClaudeCredsRepaired},
		{name: "unrecoverable", state: ClaudeCredsUnrecoverable},
		{name: "error", err: errors.New("security unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{
				ClaudeProvider: NewClaudeProvider("claude", "bypassPermissions"),
				state:          tc.state,
				err:            tc.err,
			}

			repairProviderCredentials("sess-1", p)

			if p.called != 1 {
				t.Errorf("RepairCredentials called %d times, want 1", p.called)
			}
		})
	}
}

func TestRepairProviderCredentialsSkipsNonRepairers(t *testing.T) {
	// Codex manages its own credentials; the hook must leave it alone rather
	// than block the spawn.
	repairProviderCredentials("sess-1", NewCodexProvider("codex"))
}

// TestClaudeProviderImplementsCredentialRepairer keeps the provider wired into
// the pre-spawn repair hook.
func TestClaudeProviderImplementsCredentialRepairer(t *testing.T) {
	var p Provider = NewClaudeProvider("claude", "bypassPermissions")
	if _, ok := p.(credentialRepairer); !ok {
		t.Fatal("ClaudeProvider no longer implements credentialRepairer")
	}
}

// TestNonClaudeProvidersSkipCredentialRepair documents that the repair is
// Claude-specific; other CLIs manage their own credentials.
func TestNonClaudeProvidersSkipCredentialRepair(t *testing.T) {
	for _, p := range []Provider{NewCodexProvider("codex"), NewCursorProvider("cursor-agent")} {
		if _, ok := p.(credentialRepairer); ok {
			t.Errorf("%s unexpectedly implements credentialRepairer", p.Name())
		}
	}
}
