#!/bin/bash
# PreToolUse hook: Read/Grep/Globのファイルアクセスをログに記録
# + 大きいファイル(100KB超)のReadにはlimit=500を自動注入
INPUT=$(cat /dev/stdin)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name')
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // .tool_input.path // "cwd"')
PATTERN=$(echo "$INPUT" | jq -r '.tool_input.pattern // ""')
LIMIT=$(echo "$INPUT" | jq -r '.tool_input.limit // ""')
SESSION=$(echo "$INPUT" | jq -r '.session_id')

LOG_DIR="$HOME/.claude/logs"
mkdir -p "$LOG_DIR"
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ)	$TOOL_NAME	$FILE_PATH	pattern=$PATTERN	limit=$LIMIT	session=$SESSION" >> "$LOG_DIR/file-access.log"

# Readツール: 大きいファイルにlimitを自動注入（画像ファイルはスキップ）
if [ "$TOOL_NAME" = "Read" ] && [ -z "$LIMIT" ] && [ -f "$FILE_PATH" ]; then
  EXT="${FILE_PATH##*.}"
  EXT_LOWER=$(echo "$EXT" | tr '[:upper:]' '[:lower:]')
  case "$EXT_LOWER" in
    png|jpg|jpeg|gif|webp|bmp|ico|svg|tiff|tif|avif)
      # 画像ファイルはバイナリなのでlimit注入をスキップ
      exit 0
      ;;
  esac
  FILE_SIZE=$(wc -c < "$FILE_PATH" 2>/dev/null || echo 0)
  if [ "$FILE_SIZE" -gt 100000 ]; then
    echo "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"allow\",\"updatedInput\":{\"file_path\":$(echo "$FILE_PATH" | jq -Rs .),\"limit\":500},\"additionalContext\":\"Note: File is $(( FILE_SIZE / 1024 ))KB. Reading limited to 500 lines. Use offset/limit to read other sections.\"}}"
    exit 0
  fi
fi

exit 0
