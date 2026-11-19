package claudeauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeScript stands in for the claude CLI: `auth login` prints the
// manual-login prompt on its terminal and accepts the code "good".
const fakeClaudeScript = `#!/bin/sh
if [ "$1 $2" = "auth login" ]; then
  printf "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=s\r\nPaste code here if prompted > "
  read code
  if [ "$code" = "good" ]; then echo "Login successful."; touch "$0.ok"; exit 0; fi
  echo "Couldn't log in: That code wasn't accepted."
  exit 1
fi
if [ "$1 $2" = "auth status" ]; then
  if [ -f "$0.ok" ]; then echo '{"loggedIn":true}'; exit 0; fi
  echo '{"loggedIn":false}'; exit 1
fi
exit 2
`

func writeFakeClaude(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(p, []byte(fakeClaudeScript), 0o755))
	return p
}

func TestCLI_LoginThroughPTY(t *testing.T) {
	bin := writeFakeClaude(t)
	check := CLIStatusChecker(bin)
	ok, err := check(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)

	done := false
	f := NewLoginFlow(CLIStarter(bin), check, func() { done = true })

	st, err := f.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, PhaseWaitingCode, st.Phase, st.Message)
	assert.Equal(t, "https://claude.com/cai/oauth/authorize?code=true&state=s", st.URL)

	_, err = f.SubmitCode("good")
	require.NoError(t, err)
	waitPhase(t, f, PhaseSucceeded)
	assert.True(t, done)
}

func TestCLI_RejectedCodeThroughPTY(t *testing.T) {
	bin := writeFakeClaude(t)
	f := NewLoginFlow(CLIStarter(bin), CLIStatusChecker(bin), nil)
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("bad")
	require.NoError(t, err)
	st := waitPhase(t, f, PhaseFailed)
	assert.Contains(t, st.Message, "Couldn't log in")
}
