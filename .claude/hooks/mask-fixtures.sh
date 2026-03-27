#!/bin/bash
# PostToolUse hook: mask sensitive data in scenario fixtures after Edit/Write
# Only runs when the edited file is in fixtures/scenarios/

INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')

if [[ "$FILE_PATH" == *"fixtures/scenarios/"* && "$FILE_PATH" == *.ts && "$(basename "$FILE_PATH")" != "index.ts" ]]; then
  python3 "$(dirname "$0")/../../scripts/mask-fixtures.py" >&2
fi
