package redact

import (
	"strings"
	"testing"
)

// Token fixtures are assembled at runtime rather than written as literals:
// spelled out in full they look real enough to trip GitHub's push protection,
// which blocks the push even though no actual secret is involved.
var (
	doTokenFixture     = "dop_v1_" + strings.Repeat("0123456789abcdef", 4)
	stripeSkFixture    = "sk_" + "live_" + "abcdefghijklmnopqrstuvwx"
	stripePkFixture    = "pk_" + "live_" + "abcdefghijklmnopqrstuvwx"
	sendgridKeyFixture = "SG." + strings.Repeat("a", 22) + "." + strings.Repeat("b", 22)
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "anthropic key",
			input: "key is sk-ant-abcdefghijklmnopqrstuvwxyz123456",
			want:  "key is ***",
		},
		{
			name:  "openai key",
			input: "key is sk-abcdefghijklmnopqrstuvwx",
			want:  "key is ***",
		},
		{
			name:  "github token",
			input: "token is ghp_abcdefghijklmnopqrstuvwx",
			want:  "token is ***",
		},
		{
			name:  "github pat",
			input: "token is github_pat_abcdefghijklmnopqrstuvwx",
			want:  "token is ***",
		},
		{
			name:  "gitlab token",
			input: "token is glpat-abcdefghijklmnopqrstuvwx",
			want:  "token is ***",
		},
		{
			name:  "slack token",
			input: "token is xoxb-abcdefghij",
			want:  "token is ***",
		},
		{
			name:  "aws key",
			input: "key is AKIAABCDEFGHIJKLMNOP",
			want:  "key is ***",
		},
		{
			name:  "google key",
			input: "key is AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ1234567",
			want:  "key is ***",
		},
		{
			name:  "npm token",
			input: "token is npm_abcdefghijklmnopqrstuvwxyz0123456789",
			want:  "token is ***",
		},
		{
			name:  "digitalocean token",
			input: "token is " + doTokenFixture,
			want:  "token is ***",
		},
		{
			name:  "stripe sk_live key",
			input: "key is " + stripeSkFixture,
			want:  "key is ***",
		},
		{
			name:  "stripe pk_live key",
			input: "key is " + stripePkFixture,
			want:  "key is ***",
		},
		{
			name:  "sendgrid key",
			input: "key is " + sendgridKeyFixture,
			want:  "key is ***",
		},
		{
			name:  "huggingface token",
			input: "token is hf_abcdefghijklmnopqrstuvwxyz01234567890",
			want:  "token is ***",
		},
		{
			name:  "jwt",
			input: "jwt is eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dQw4w9WgXcQ_signature123",
			want:  "jwt is ***",
		},
		{
			name:  "flag token space separated",
			input: "--token abc123def456",
			want:  "--token ***",
		},
		{
			name:  "flag token equals separated",
			input: "--token=abc123def456",
			want:  "--token=***",
		},
		{
			name:  "flag value quoted",
			input: `--value "sk-ant-xxxxxxxxxxxxxxxxxxxxxxxx"`,
			want:  "--value ***",
		},
		{
			name:  "flag value env ref double quoted unchanged",
			input: `--value "$SECRET"`,
			want:  `--value "$SECRET"`,
		},
		{
			name:  "flag value env ref bare unchanged",
			input: "--value $SECRET",
			want:  "--value $SECRET",
		},
		{
			name:  "assignment github token",
			input: "export GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxx",
			want:  "export GITHUB_TOKEN=***",
		},
		{
			name:  "authorization bearer header",
			input: "Authorization: Bearer xyz123abc",
			want:  "Authorization: Bearer ***",
		},
		{
			name:  "no false positive ls -la",
			input: "ls -la",
			want:  "ls -la",
		},
		{
			name:  "no false positive git status",
			input: "git status",
			want:  "git status",
		},
		{
			name:  "no false positive mkdir -p",
			input: "mkdir -p /tmp/foo",
			want:  "mkdir -p /tmp/foo",
		},
		{
			name:  "no false positive make build",
			input: "make build",
			want:  "make build",
		},
		{
			name:  "no false positive echo hello",
			input: "echo hello",
			want:  "echo hello",
		},
		{
			name:  "secret context redacts --body value",
			input: `gh secret set API_KEY --body "abc123randomvalue"`,
			want:  "gh secret set API_KEY --body ***",
		},
		{
			name:  "secret context redacts piped echo argument",
			input: `echo "a1b2c3d4e5f6g7h8" | wrangler secret put API_KEY`,
			want:  "echo *** | wrangler secret put API_KEY",
		},
		{
			name:  "secret context redacts assignment without secret-ish name",
			input: "fly secrets set DATABASE_URL=postgres://u:p@h/db",
			want:  "fly secrets set DATABASE_URL=***",
		},
		{
			name:  "secret context keeps env var reference",
			input: `wrangler secret put API_KEY --value "$MY_TOKEN"`,
			want:  `wrangler secret put API_KEY --value "$MY_TOKEN"`,
		},
		{
			name:  "no secret context leaves --body alone",
			input: `gh issue create --body "普通のイシュー本文"`,
			want:  `gh issue create --body "普通のイシュー本文"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
