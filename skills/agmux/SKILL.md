---
name: agmux
description: Refer to this skill when you need to manage sessions, create new AI agent projects, or manage and implement tasks using AI agents.
user-invocable: false
---

# agmux

A unified CLI tool for running, monitoring, and controlling multiple AI agent sessions on tmux.

## Common Commands

### Server

```bash
agmux serve                # Start server (daemon + web UI)
agmux serve -p 8080        # Specify port
agmux serve --dev          # Dev mode (enable CORS)
```

### Session Management

```bash
agmux session list                          # List all sessions
agmux session create <name> -p <path>       # Create session with project directory
agmux session create <name> -m "prompt"     # Create session with initial prompt
agmux session send <id> "message"           # Send message to a session
agmux session stop <id>                     # Stop a session
agmux session delete <id>                   # Delete a session
```
