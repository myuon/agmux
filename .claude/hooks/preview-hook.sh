#!/bin/bash
# Hook script for automatic preview environment management.
# Triggered as a PostToolUse hook on Bash commands.
# Detects gh pr create/merge/close and runs make preview/preview-stop accordingly.

set -o pipefail

# Read JSON from stdin
INPUT=$(cat)

TOOL_INPUT_COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')
TOOL_OUTPUT=$(echo "$INPUT" | jq -r '.tool_output // empty')

# Determine the repo root (two levels up from this script's directory)
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

# --- PR Create ---
if echo "$TOOL_INPUT_COMMAND" | grep -q 'gh pr create'; then
  PR_NUMBER=$(echo "$TOOL_OUTPUT" | grep -oE 'pull/[0-9]+' | head -1 | grep -oE '[0-9]+')
  if [ -n "$PR_NUMBER" ]; then
    (cd "$REPO_ROOT" && make preview PR="$PR_NUMBER" > /dev/null 2>&1 &)
  fi
  exit 0
fi

# --- PR Merge ---
if echo "$TOOL_INPUT_COMMAND" | grep -q 'gh pr merge'; then
  PR_NUMBER=$(echo "$TOOL_INPUT_COMMAND" | grep -oE 'gh pr merge\s+[0-9]+' | grep -oE '[0-9]+')
  if [ -z "$PR_NUMBER" ]; then
    PR_NUMBER=$(echo "$TOOL_OUTPUT" | grep -oE 'pull/[0-9]+' | head -1 | grep -oE '[0-9]+')
  fi
  if [ -n "$PR_NUMBER" ]; then
    (cd "$REPO_ROOT" && make preview-stop PR="$PR_NUMBER" > /dev/null 2>&1 &)
  fi
  exit 0
fi

# --- PR Close ---
if echo "$TOOL_INPUT_COMMAND" | grep -q 'gh pr close'; then
  PR_NUMBER=$(echo "$TOOL_INPUT_COMMAND" | grep -oE 'gh pr close\s+[0-9]+' | grep -oE '[0-9]+')
  if [ -z "$PR_NUMBER" ]; then
    PR_NUMBER=$(echo "$TOOL_OUTPUT" | grep -oE 'pull/[0-9]+' | head -1 | grep -oE '[0-9]+')
  fi
  if [ -n "$PR_NUMBER" ]; then
    (cd "$REPO_ROOT" && make preview-stop PR="$PR_NUMBER" > /dev/null 2>&1 &)
  fi
  exit 0
fi
