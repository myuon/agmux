// Event that opens the Claude re-login dialog from anywhere (e.g. Settings).
export const OPEN_CLAUDE_LOGIN_EVENT = "agmux:open-claude-login";

export function openClaudeLogin() {
  window.dispatchEvent(new Event(OPEN_CLAUDE_LOGIN_EVENT));
}
