package claudeauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Built from parts so the fixture does not look like a real token to secret
// scanners.
var testToken = "sk-ant-" + "oat01-" + "dummyTOKEN_value-0123456789abcdefghijklmnop"

// inkWords lays words out the way the setup-token screen does: with cursor
// moves instead of spaces.
func inkWords(s string) string {
	words := strings.Fields(s)
	var b strings.Builder
	col := 2
	for i, w := range words {
		if i > 0 {
			b.WriteString("\x1b[" + itoa(col) + "G")
		}
		b.WriteString(w)
		col += len(w) + 1
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// printSetupTokenPrompt prints what `claude setup-token` showed on a PTY
// (Claude Code 2.1.327), up to the code box.
func printSetupTokenPrompt(p *fakeProc) {
	p.print("\x1b[?25l" + inkWords("Welcome to Claude Code v2.1.327") + "\r\r\n" +
		inkWords("This will guide you through long-lived (1-year) auth token setup for your Claude account. Claude subscription required.") + "\r\r\n" +
		"\x1b[2G·\x1b[4G" + inkWords("Opening browser to sign in…") + "\r\r\n" +
		inkWords("Browser didn't open? Use the URL below to sign in (c to copy)") + "\r\r\n\r\r\n" +
		"\x1b]8;id=1;" + testURL + "\x07" + testURL + "\x1b]8;;\x07\r\r\n\r\r\n" +
		"\x1b[2G" + inkWords("Paste code here if prompted") + "\r\r\n\x1b[2G╭────╮\r\r\n\x1b[2G│\x1b[4G\x1b[7m \x1b[27m│\r\r\n\x1b[2G╰────╯\r\r\n")
}

func successScreen(token string) string {
	return "\x1b[2G✓ " + inkWords("Long-lived authentication token created successfully!") + "\r\r\n\r\r\n" +
		"\x1b[2G" + inkWords("Your OAuth token (valid for 1 year):") + "\r\r\n\r\r\n" +
		"\x1b[2G" + token + "\r\r\n\r\r\n" +
		"\x1b[2G" + inkWords("Store this token securely. You won't be able to see it again.") + "\r\r\n" +
		"\x1b[2G" + inkWords("Use this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>") + "\r\r\n"
}

func TestExtractSetupToken(t *testing.T) {
	assert.Equal(t, testToken, ExtractSetupToken(successScreen(testToken)))
	// The token's prefix alone, or a token at the very end (possibly cut
	// mid-read), is not taken.
	assert.Empty(t, ExtractSetupToken("\x1b[2GYour\x1b[7GOAuth\x1b[13Gtoken:\r\n"+testToken[:20]))
	assert.Empty(t, ExtractSetupToken("Your OAuth token (valid for 1 year):\r\n"+testToken))
	// Only after the marker: the export hint's <token> or other sk- strings
	// earlier on the screen do not count.
	assert.Empty(t, ExtractSetupToken(testToken+"\r\nno marker\r\n"))
	assert.Empty(t, ExtractSetupToken("Your OAuth token (valid for 1 year):\r\n\r\nStore this token securely.\r\n"))
}

func TestSetupTokenFailure(t *testing.T) {
	out := "\r\x1b[1C\x1b[1A" + inkWords("OAuth error: That code wasn't accepted. It may be incomplete, expired, already used, or from an earlier sign-in attempt. Sign in again to get a new code.") +
		"\r\r\n\r\r\n\x1b[2G" + inkWords("Press Enter to retry.") + "\r\r\n"
	tok, fail := scanSetupToken(out)
	assert.Empty(t, tok)
	assert.True(t, strings.HasPrefix(fail, "OAuth error: That code wasn't accepted."), fail)

	_, fail = scanSetupToken(inkWords("Something else went wrong") + "\r\n" + inkWords("Press Enter to retry.") + "\r\n")
	assert.Contains(t, fail, "失敗")

	// Nothing decided while the CLI is still working.
	tok, fail = scanSetupToken("✻ Finishing sign-in…\r\n")
	assert.Empty(t, tok)
	assert.Empty(t, fail)
}

func TestTokenExpiry(t *testing.T) {
	created := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2027, 1, 10, 9, 0, 0, 0, time.UTC), TokenExpiry(created))
	assert.False(t, TokenExpiringSoon(created, created.Add(350*24*time.Hour)))
	assert.True(t, TokenExpiringSoon(created, created.Add(351*24*time.Hour)))
	assert.True(t, TokenExpiringSoon(created, created.Add(400*24*time.Hour)))
}

func newTestTokenFlow(proc *fakeProc, save func(string) error, onSuccess func()) *LoginFlow {
	f := NewTokenFlow(func() (Process, error) { go printSetupTokenPrompt(proc); return proc, nil }, save, onSuccess)
	f.SubmitDelay = time.Millisecond
	return f
}

func TestTokenFlow_Success(t *testing.T) {
	proc := newFakeProc()
	var saved string
	succeeded := false
	f := newTestTokenFlow(proc, func(tok string) error { saved = tok; return nil }, func() { succeeded = true })

	st, err := f.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, PhaseWaitingCode, st.Phase)
	assert.Equal(t, testURL, st.URL)

	_, err = f.SubmitCode("code#state")
	require.NoError(t, err)
	// The code and Enter are typed separately.
	assert.Equal(t, "code#state\r", proc.typed())

	proc.print("\x1b[1A✻ Finishing sign-in…\r\n")
	assert.Equal(t, PhaseVerifying, f.Status().Phase)
	// The CLI does not exit by itself; agmux ends it once the token is read.
	go proc.print(successScreen(testToken))
	st = waitPhase(t, f, PhaseSucceeded)
	assert.Equal(t, testToken, saved)
	assert.True(t, succeeded)
	assert.True(t, proc.wasKilled())
	assert.NotContains(t, st.Message, testToken)
	assert.NotContains(t, st.URL, testToken)
}

func TestTokenFlow_RejectedCode(t *testing.T) {
	proc := newFakeProc()
	f := newTestTokenFlow(proc, func(string) error { t.Error("nothing must be saved"); return nil }, func() { t.Error("onSuccess must not run") })
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("wrongcode#state")
	require.NoError(t, err)
	go proc.print("\r\x1b[1C\x1b[1A" + inkWords("OAuth error: That code wasn't accepted. Sign in again to get a new code.") + "\r\r\n\x1b[2G" + inkWords("Press Enter to retry.") + "\r\r\n")
	st := waitPhase(t, f, PhaseFailed)
	assert.Contains(t, st.Message, "That code wasn't accepted")
	assert.NotContains(t, st.Message, "wrongcode")
	assert.True(t, proc.wasKilled(), "the CLI waiting for Enter is killed")
}

func TestTokenFlow_UnreadableToken(t *testing.T) {
	proc := newFakeProc()
	f := newTestTokenFlow(proc, func(string) error { t.Error("nothing must be saved"); return nil }, nil)
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("code")
	require.NoError(t, err)
	// A format agmux does not know, then the CLI exits.
	proc.print("Your OAuth token (valid for 1 year):\r\n\r\nsomething-unexpected\r\n")
	proc.exit(nil)
	st := waitPhase(t, f, PhaseFailed)
	assert.Equal(t, TokenUnreadableMessage, st.Message)
}

func TestTokenFlow_SaveError(t *testing.T) {
	proc := newFakeProc()
	f := newTestTokenFlow(proc, func(string) error { return errors.New("keychain locked") }, func() { t.Error("onSuccess must not run") })
	_, err := f.Start(context.Background())
	require.NoError(t, err)
	_, err = f.SubmitCode("code")
	require.NoError(t, err)
	go proc.print(successScreen(testToken))
	st := waitPhase(t, f, PhaseFailed)
	assert.Contains(t, st.Message, "keychain locked")
	assert.NotContains(t, st.Message, testToken)
}
