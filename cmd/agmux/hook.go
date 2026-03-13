package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Hook commands for Claude Code integration",
		Hidden: true,
	}

	cmd.AddCommand(readOnlyGuardCmd())
	return cmd
}

// preToolUseInput represents the JSON input from Claude Code's preToolUse hook.
type preToolUseInput struct {
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"tool_input"`
}

// hookDecision represents the JSON output for Claude Code hooks.
type hookDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func readOnlyGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "read-only-guard",
		Short:  "preToolUse hook that blocks file modifications in read-only sessions",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}

			var hook preToolUseInput
			if err := json.Unmarshal(input, &hook); err != nil {
				// If we can't parse input, approve by default
				return outputDecision(hookDecision{Decision: "approve"})
			}

			switch hook.ToolName {
			case "Edit", "Write", "NotebookEdit":
				return outputDecision(hookDecision{
					Decision: "block",
					Reason:   "Read-only session: file modifications are not allowed",
				})
			case "Bash":
				return handleBashGuard(hook.Input)
			default:
				// All other tools (Read, Grep, Glob, Agent, etc.) are allowed
				return outputDecision(hookDecision{Decision: "approve"})
			}
		},
	}
}

func handleBashGuard(rawInput json.RawMessage) error {
	var bashInput struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rawInput, &bashInput); err != nil {
		// Can't parse, approve by default
		return outputDecision(hookDecision{Decision: "approve"})
	}

	command := bashInput.Command
	if isDestructiveBashCommand(command) {
		return outputDecision(hookDecision{
			Decision: "block",
			Reason:   "Read-only session: file modifications are not allowed",
		})
	}

	return outputDecision(hookDecision{Decision: "approve"})
}

// blockedCommands are commands that modify the filesystem.
var blockedCommands = []string{
	"rm", "mv", "cp", "sed", "awk", "tee",
	"chmod", "chown", "mkdir", "rmdir", "touch",
	"truncate", "dd", "install",
}

// allowedCommands are safe commands that should never be blocked.
var allowedCommands = []string{
	"gh", "git", "ls", "cat", "head", "tail",
	"echo", "printf", "grep", "find", "which",
	"pwd", "cd", "env", "export", "make", "go",
	"npm", "npx", "node", "python", "curl", "wget",
	"jq", "wc", "sort", "uniq", "diff", "date", "whoami",
}

func isDestructiveBashCommand(command string) bool {
	// Check for redirections (file write operators)
	if strings.Contains(command, ">>") || containsRedirect(command) {
		return true
	}

	// Extract the first command word from each piped/chained segment
	segments := splitCommandSegments(command)
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}

		// Strip leading env assignments (e.g. "FOO=bar cmd")
		cmdWord := extractCommandWord(seg)
		if cmdWord == "" {
			continue
		}

		// Check if it's explicitly allowed
		if isAllowedCommand(cmdWord) {
			continue
		}

		// Check if it's explicitly blocked
		if isBlockedCommand(cmdWord) {
			return true
		}
	}

	return false
}

// containsRedirect checks for > redirect operator (but not >>).
// We need to be careful not to match >> which is already checked,
// and not to match things like grep patterns.
func containsRedirect(command string) bool {
	for i := 0; i < len(command); i++ {
		if command[i] == '>' {
			// Check it's not >> (already handled above)
			if i+1 < len(command) && command[i+1] == '>' {
				return true // >> is also destructive
			}
			// Check it's not part of a heredoc <<
			if i > 0 && command[i-1] == '<' {
				continue
			}
			return true
		}
	}
	return false
}

// splitCommandSegments splits a command by pipes, &&, ||, and ;
func splitCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteByte(ch)
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteByte(ch)
		case !inSingle && !inDouble && ch == '|':
			if i+1 < len(command) && command[i+1] == '|' {
				// ||
				segments = append(segments, current.String())
				current.Reset()
				i++ // skip next |
			} else {
				// |
				segments = append(segments, current.String())
				current.Reset()
			}
		case !inSingle && !inDouble && ch == '&':
			if i+1 < len(command) && command[i+1] == '&' {
				segments = append(segments, current.String())
				current.Reset()
				i++ // skip next &
			} else {
				current.WriteByte(ch)
			}
		case !inSingle && !inDouble && ch == ';':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	return segments
}

// extractCommandWord gets the first actual command word, skipping env assignments and sudo/nohup.
func extractCommandWord(segment string) string {
	fields := strings.Fields(segment)
	for _, f := range fields {
		// Skip env var assignments
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
			continue
		}
		// Skip sudo, nohup, etc.
		if f == "sudo" || f == "nohup" || f == "nice" || f == "time" {
			continue
		}
		// Get basename (e.g. /usr/bin/rm -> rm)
		parts := strings.Split(f, "/")
		return parts[len(parts)-1]
	}
	return ""
}

func isAllowedCommand(cmd string) bool {
	for _, c := range allowedCommands {
		if cmd == c {
			return true
		}
	}
	return false
}

func isBlockedCommand(cmd string) bool {
	for _, c := range blockedCommands {
		if cmd == c {
			return true
		}
	}
	return false
}

func outputDecision(d hookDecision) error {
	return json.NewEncoder(os.Stdout).Encode(d)
}
