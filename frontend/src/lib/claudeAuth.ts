// Event that opens the Claude re-login dialog from anywhere (e.g. Settings).
export const OPEN_CLAUDE_LOGIN_EVENT = "agmux:open-claude-login";

export function openClaudeLogin() {
  window.dispatchEvent(new Event(OPEN_CLAUDE_LOGIN_EVENT));
}

// Event that opens the long-lived token (claude setup-token) dialog.
export const OPEN_CLAUDE_TOKEN_EVENT = "agmux:open-claude-token";

export function openClaudeToken() {
  window.dispatchEvent(new Event(OPEN_CLAUDE_TOKEN_EVENT));
}

// YYYY-MM-DD in local time.
export function formatDay(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
