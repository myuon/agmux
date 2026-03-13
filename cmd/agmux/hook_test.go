package main

import "testing"

func TestIsDestructiveBashCommand(t *testing.T) {
	tests := []struct {
		command     string
		destructive bool
	}{
		// Blocked commands
		{"rm -rf /tmp/foo", true},
		{"mv file1 file2", true},
		{"cp file1 file2", true},
		{"sed -i 's/foo/bar/' file", true},
		{"tee output.txt", true},
		{"touch newfile", true},
		{"mkdir -p /tmp/foo", true},
		{"chmod 755 file", true},
		{"truncate -s 0 file", true},

		// Redirects
		{"echo hello > file.txt", true},
		{"echo hello >> file.txt", true},

		// Allowed commands
		{"gh issue list", false},
		{"git status", false},
		{"git diff", false},
		{"ls -la", false},
		{"cat file.txt", false},
		{"grep foo file.txt", false},
		{"find . -name '*.go'", false},
		{"pwd", false},
		{"make test", false},
		{"go test ./...", false},
		{"npm run test", false},
		{"curl https://example.com", false},
		{"jq '.foo' file.json", false},
		{"wc -l file.txt", false},

		// Pipe with safe commands
		{"git log | head -20", false},
		{"cat file.txt | grep foo", false},

		// Chained with safe commands
		{"git status && git diff", false},

		// Mixed: blocked in chain
		{"git status && rm file", true},
		{"ls && cp file1 file2", true},

		// Env vars prefix
		{"FOO=bar ls", false},
		{"FOO=bar rm file", true},

		// Sudo
		{"sudo rm -rf /", true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := isDestructiveBashCommand(tt.command)
			if got != tt.destructive {
				t.Errorf("isDestructiveBashCommand(%q) = %v, want %v", tt.command, got, tt.destructive)
			}
		})
	}
}
