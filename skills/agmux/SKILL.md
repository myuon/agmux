---
name: agmux
description: Refer to this skill when you need to manage sessions, create new AI agent projects, or manage and implement tasks using AI agents via the agmux CLI.
user-invocable: false
---

# agmux

A unified CLI tool for running, monitoring, and controlling multiple AI agent sessions.

## Common Commands

### Server

```bash
agmux serve                # Start server (default port from config or 4321)
agmux serve -p 8080        # Specify port
agmux serve --dev          # Dev mode (enable CORS for Vite)
```

### Session Management

```bash
agmux session list                          # List all sessions
agmux session create <name>                 # Create session
agmux session create <name> -p <path>       # Create session with project directory
agmux session create <name> -m "prompt"     # Create session with initial prompt
agmux session create <name> --model claude-sonnet-4-5  # Specify model
agmux session create <name> --provider codex           # Use codex provider (default: claude)
agmux session create <name> -w              # Create a git worktree for the session
agmux session send <id> "message"           # Send message to a session
agmux session stop <id>                     # Stop a session
agmux session delete <id>                   # Delete a session
agmux session capture <id>                  # Capture session output
```

### Logs

```bash
agmux logs <session-id>          # Show session stream log
agmux logs <session-id> -f       # Follow log output in realtime
agmux logs <session-id> -n 50    # Show last 50 lines (default: 20)
agmux logs --server              # Show server log
agmux logs --daemon              # Show daemon log
```

### Daemon

```bash
agmux daemon install     # Install daemon (launchd)
agmux daemon uninstall   # Uninstall daemon
```
