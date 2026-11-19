package claudeauth

import (
	"regexp"
	"strings"
	"time"
)

// Long-lived token (`claude setup-token`, #833).
//
// `claude setup-token` (Claude Code 2.1.327, 2026-11) is a full-screen Ink
// app. On a terminal it prints the same manual-login URL as `auth login`
// (also as an OSC 8 hyperlink, with scope=user:inference) and a
// "Paste code here if prompted" text box. Words are placed with cursor
// movements (ESC[<n>G) rather than spaces. A rejected code prints
//
//	OAuth error: That code wasn't accepted. It may be incomplete, ...
//	Press Enter to retry.
//
// and the process keeps running. On success it prints (strings taken from
// the CLI binary; not observed, since that needs a real approval)
//
//	Long-lived authentication token created ...
//	Your OAuth token (valid for 1 year):
//	sk-ant-oat01-...
//	Store this token securely. You won't be able to see it again.
//	Use this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>
//
// The token is the secret: it goes straight to the secret store and is never
// logged, returned by the API, or shown to the user.

// TokenSecretName is the secret (and env var) holding the long-lived token.
const TokenSecretName = "CLAUDE_CODE_OAUTH_TOKEN"

// TokenLifetime is how long a setup-token token is valid.
const TokenLifetime = 365 * 24 * time.Hour

// TokenWarnBefore is how long before the expiry the user is told to renew.
const TokenWarnBefore = 14 * 24 * time.Hour

// TokenUnreadableMessage is shown when the CLI ended without a token agmux
// could read. Nothing is saved then.
const TokenUnreadableMessage = "トークンを読み取れませんでした。保存していません。もう一度やり直してください。"

// TokenExpiry returns when a token created at createdAt expires.
func TokenExpiry(createdAt time.Time) time.Time { return createdAt.Add(TokenLifetime) }

// TokenExpiringSoon reports whether a token created at createdAt should be
// renewed now.
func TokenExpiringSoon(createdAt, now time.Time) bool {
	return !now.Before(TokenExpiry(createdAt).Add(-TokenWarnBefore))
}

// NewTokenFlow creates a flow running `claude setup-token`. save stores the
// token; onSuccess runs after it was saved.
func NewTokenFlow(start Starter, save func(token string) error, onSuccess func()) *LoginFlow {
	f := NewLoginFlow(start, nil, onSuccess)
	f.token = true
	f.saveToken = save
	f.label = "claude setup-token"
	f.SubmitDelay = 300 * time.Millisecond
	return f
}

var (
	// Cursor moves stand in for spaces in Ink's output.
	cursorMoveRe = regexp.MustCompile(`\x1b\[[0-9]*[CG]`)
	setupTokenRe = regexp.MustCompile(`sk-ant-[a-z]+[0-9]*-[A-Za-z0-9_\-]{20,}`)
)

const tokenMarker = "Your OAuth token"

// normalizeScreen turns the CLI's terminal output into plain text.
func normalizeScreen(out string) string {
	out = cursorMoveRe.ReplaceAllString(out, " ")
	return strings.ReplaceAll(StripANSI(out), "\r", "")
}

// ExtractSetupToken returns the token printed after "Your OAuth token", or ""
// when there is none yet. The token must be followed by more output, so a
// token cut in the middle of a read is not taken.
func ExtractSetupToken(out string) string {
	text := normalizeScreen(out)
	i := strings.Index(text, tokenMarker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(tokenMarker):]
	loc := setupTokenRe.FindStringIndex(rest)
	if loc == nil || loc[1] >= len(rest) {
		return ""
	}
	return rest[loc[0]:loc[1]]
}

// setupTokenFailure returns the CLI's error line ("OAuth error: ..."), or "".
func setupTokenFailure(out string) string {
	for _, l := range strings.Split(normalizeScreen(out), "\n") {
		l = strings.Join(strings.Fields(l), " ")
		for _, p := range []string{"OAuth error:", "Couldn't log in"} {
			if j := strings.Index(l, p); j >= 0 && !strings.Contains(l, "http") {
				return l[j:]
			}
		}
	}
	return ""
}

// scanSetupToken inspects the output after the code was sent.
func scanSetupToken(out string) (token, failure string) {
	if t := ExtractSetupToken(out); t != "" {
		return t, ""
	}
	text := normalizeScreen(out)
	if strings.Contains(text, tokenMarker) {
		return "", ""
	}
	if m := setupTokenFailure(out); m != "" {
		return "", m
	}
	if strings.Contains(strings.Join(strings.Fields(text), " "), "Press Enter to retry") {
		return "", "claude setup-token が失敗しました。もう一度やり直してください。"
	}
	return "", ""
}

// finishToken ends a setup-token attempt: it saves tok, or fails with fail.
func (f *LoginFlow) finishToken(gen int, tok, fail string) {
	f.mu.Lock()
	if f.gen != gen || !f.phase.active() {
		f.mu.Unlock()
		return
	}
	if tok == "" {
		if fail == "" {
			fail = TokenUnreadableMessage
		}
		f.phase, f.message, f.url = PhaseFailed, fail, ""
		f.endLocked()
		f.mu.Unlock()
		return
	}
	f.phase, f.url = PhaseVerifying, ""
	f.output = nil // drop the copy of the token
	f.mu.Unlock()

	var err error
	if f.saveToken != nil {
		err = f.saveToken(tok)
	}
	tok = ""
	if err == nil && f.onSuccess != nil {
		f.onSuccess()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gen != gen || f.phase != PhaseVerifying {
		return
	}
	if err != nil {
		f.phase, f.message = PhaseFailed, "トークンを保存できませんでした: "+err.Error()
	} else {
		f.phase, f.message = PhaseSucceeded, "長期トークンを作成して保存しました。"
	}
	f.endLocked()
}
