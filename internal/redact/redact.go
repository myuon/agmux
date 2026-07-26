// Package redact provides best-effort secret redaction for command outputs
// recorded in "command execution mode" logs.
package redact

import (
	"regexp"
	"strings"
)

// Category A: known-prefix tokens. Entire token is replaced with "***".
var (
	anthropicKeyRe      = regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)
	openAIKeyRe         = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`)
	githubTokenRe       = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)
	githubPatRe         = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)
	gitlabTokenRe       = regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`)
	slackTokenRe        = regexp.MustCompile(`xox[baprs]-[A-Za-z0-9\-]{10,}`)
	awsKeyRe            = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	googleKeyRe         = regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)
	npmTokenRe          = regexp.MustCompile(`npm_[A-Za-z0-9]{36}`)
	digitalOceanTokenRe = regexp.MustCompile(`dop_v1_[a-f0-9]{64}`)
	stripeKeyRe         = regexp.MustCompile(`(sk|rk|pk)_live_[A-Za-z0-9]{20,}`)
	sendgridKeyRe       = regexp.MustCompile(`SG\.[A-Za-z0-9_\-]{20,}\.[A-Za-z0-9_\-]{20,}`)
	huggingfaceTokenRe  = regexp.MustCompile(`hf_[A-Za-z0-9]{30,}`)
	jwtRe               = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)
)

// Category B: flag values, e.g. --token VALUE or --token=VALUE.
var flagValueRe = regexp.MustCompile(`(--(?:value|token|password|secret|api-key|apikey|access-token|auth|key))(=|\s+)("(?:[^"]*)"|'(?:[^']*)'|\S+)`)

// Category C: shell-style assignment, e.g. NAME=VALUE.
var assignRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)(=)("(?:[^"]*)"|'(?:[^']*)'|\S+)`)

// Category D: Authorization header.
var authHeaderRe = regexp.MustCompile(`(?i)(Authorization:\s*)(Bearer|Basic|token)(\s+)[^\s"']+`)

// Category E: "secret context" rules. Secret-registration commands
// (gh secret set, wrangler secret put, fly secrets set, ...) pass the value as
// an opaque random string that carries no recognizable prefix, so categories
// A-C miss it. When the text mentions "secret"/"secrets" we therefore redact
// far more aggressively: over-redacting a command that is literally about
// secrets costs nothing, while the same rules stay off for unrelated commands
// such as `gh issue create --body "..."`.
var (
	secretContextRe = regexp.MustCompile(`(?i)\bsecrets?\b`)
	bodyFlagRe      = regexp.MustCompile(`(--body)(=|\s+)("(?:[^"]*)"|'(?:[^']*)'|\S+)`)
	anyAssignRe     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)(=)("(?:[^"]*)"|'(?:[^']*)'|\S+)`)
	echoArgRe       = regexp.MustCompile(`\b(echo)(\s+)("(?:[^"]*)"|'(?:[^']*)'|\S+)`)
)

// redactValueGroups replaces the third submatch (the value) of re with "***",
// keeping the first two submatches (name and separator) intact. Values that
// reference an environment variable are left alone.
func redactValueGroups(s string, re *regexp.Regexp) string {
	return re.ReplaceAllStringFunc(s, func(match string) string {
		groups := re.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		if strings.HasPrefix(stripQuotes(groups[3]), "$") {
			return match
		}
		return groups[1] + groups[2] + "***"
	})
}

var assignKeywords = []string{
	"TOKEN",
	"SECRET",
	"PASSWORD",
	"PASSWD",
	"API_KEY",
	"APIKEY",
	"ACCESS_KEY",
	"PRIVATE_KEY",
	"CREDENTIAL",
	"AUTH",
}

// stripQuotes removes a single layer of surrounding double or single quotes
// from raw, if present, returning the unquoted value.
func stripQuotes(raw string) string {
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// Redact applies best-effort secret redaction to s, replacing detected
// secret substrings with "***". Empty input returns empty output.
func Redact(s string) string {
	if s == "" {
		return s
	}

	// Category A: known-prefix tokens, entire token replaced.
	s = anthropicKeyRe.ReplaceAllString(s, "***")
	s = openAIKeyRe.ReplaceAllString(s, "***")
	s = githubTokenRe.ReplaceAllString(s, "***")
	s = githubPatRe.ReplaceAllString(s, "***")
	s = gitlabTokenRe.ReplaceAllString(s, "***")
	s = slackTokenRe.ReplaceAllString(s, "***")
	s = awsKeyRe.ReplaceAllString(s, "***")
	s = googleKeyRe.ReplaceAllString(s, "***")
	s = npmTokenRe.ReplaceAllString(s, "***")
	s = digitalOceanTokenRe.ReplaceAllString(s, "***")
	s = stripeKeyRe.ReplaceAllString(s, "***")
	s = sendgridKeyRe.ReplaceAllString(s, "***")
	s = huggingfaceTokenRe.ReplaceAllString(s, "***")
	s = jwtRe.ReplaceAllString(s, "***")

	// Category B: flag values.
	s = flagValueRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := flagValueRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		flag, sep, rawValue := groups[1], groups[2], groups[3]
		value := stripQuotes(rawValue)
		if strings.HasPrefix(value, "$") {
			return match
		}
		return flag + sep + "***"
	})

	// Category C: shell-style assignment.
	s = assignRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := assignRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		name, eq, rawValue := groups[1], groups[2], groups[3]
		upperName := strings.ToUpper(name)
		matched := false
		for _, kw := range assignKeywords {
			if strings.Contains(upperName, kw) {
				matched = true
				break
			}
		}
		if !matched {
			return match
		}
		value := stripQuotes(rawValue)
		if strings.HasPrefix(value, "$") {
			return match
		}
		return name + eq + "***"
	})

	// Category D: Authorization header.
	s = authHeaderRe.ReplaceAllString(s, "${1}${2}${3}***")

	// Category E: aggressive rules, only for secret-registration commands.
	if secretContextRe.MatchString(s) {
		s = redactValueGroups(s, bodyFlagRe)
		s = redactValueGroups(s, echoArgRe)
		s = redactValueGroups(s, anyAssignRe)
	}

	return s
}
