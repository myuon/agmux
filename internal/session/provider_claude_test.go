package session

import (
	"testing"

	"github.com/google/uuid"
)

func TestClaudeProvider_Name(t *testing.T) {
	p := NewClaudeProvider("", "")
	if p.Name() != ProviderClaude {
		t.Errorf("expected %q, got %q", ProviderClaude, p.Name())
	}
}

func TestClaudeProvider_IsOneShot(t *testing.T) {
	p := NewClaudeProvider("", "")
	if p.IsOneShot() {
		t.Errorf("expected ClaudeProvider.IsOneShot() = false, got true")
	}
}

// flagValue returns the value following the given flag in an args slice, or
// ("", false) if the flag is not present.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// TestClaudeProvider_BuildStreamCommand_NoResume_IgnoresStaleCLISessionID is a
// regression test for issue #704: when Resume is false, a stale/already-used
// CLISessionID (e.g. resurrected from the JSONL stream after Clear()) must
// NOT be passed to --session-id, since the Claude CLI rejects reusing a
// session id that already has a transcript ("Session ID ... is already in
// use."). Instead a fresh UUID must be generated for --session-id.
func TestClaudeProvider_BuildStreamCommand_NoResume_IgnoresStaleCLISessionID(t *testing.T) {
	p := NewClaudeProvider("", "")
	staleID := "06c52c95-c82c-439a-8568-61b8541890f7"

	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "agmux-session-id",
		ProjectPath:  "/tmp/project",
		Resume:       false,
		CLISessionID: staleID,
	})

	args := cmd.Args
	flag, hasResume := flagValue(args, "--resume")
	if hasResume {
		t.Errorf("expected no --resume flag when Resume=false, got --resume %q (args: %v)", flag, args)
	}

	sessionIDValue, hasSessionID := flagValue(args, "--session-id")
	if !hasSessionID {
		t.Fatalf("expected --session-id flag to be present, args: %v", args)
	}
	if sessionIDValue == staleID {
		t.Errorf("--session-id must not reuse the stale CLISessionID %q when Resume=false, args: %v", staleID, args)
	}
	if _, err := uuid.Parse(sessionIDValue); err != nil {
		t.Errorf("--session-id value %q is not a valid UUID: %v", sessionIDValue, err)
	}
}

// TestClaudeProvider_BuildStreamCommand_Resume_UsesCLISessionID verifies the
// legitimate resume path still works: when Resume=true and CLISessionID is
// set, it must be passed via --resume (not --session-id).
func TestClaudeProvider_BuildStreamCommand_Resume_UsesCLISessionID(t *testing.T) {
	p := NewClaudeProvider("", "")
	cliID := "06c52c95-c82c-439a-8568-61b8541890f7"

	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "agmux-session-id",
		ProjectPath:  "/tmp/project",
		Resume:       true,
		CLISessionID: cliID,
	})

	args := cmd.Args
	resumeValue, hasResume := flagValue(args, "--resume")
	if !hasResume {
		t.Fatalf("expected --resume flag to be present when Resume=true and CLISessionID set, args: %v", args)
	}
	if resumeValue != cliID {
		t.Errorf("--resume value = %q, want %q", resumeValue, cliID)
	}
	if _, hasSessionID := flagValue(args, "--session-id"); hasSessionID {
		t.Errorf("did not expect --session-id flag when resuming via --resume, args: %v", args)
	}
}
