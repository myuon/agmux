import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { ClaudeTokenInfo } from "../api/client";
import { useWebSocket } from "../hooks/useWebSocket";
import { formatDay, openClaudeToken } from "../lib/claudeAuth";

// Settings row for the long-lived token (experimental "claude_relogin"):
// its state and the create / re-create / delete buttons.
export function ClaudeTokenSummary() {
  const [token, setToken] = useState<ClaudeTokenInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.getClaudeAuthStatus().then((s) => setToken(s.token ?? null)).catch(() => {});
  }, []);
  useEffect(load, [load]);
  useWebSocket(
    useCallback((msg: { type: string; data: unknown }) => {
      if (msg.type === "claude_auth") {
        const t = (msg.data as { token?: ClaudeTokenInfo }).token;
        if (t) setToken(t);
      }
    }, []),
  );

  const remove = async () => {
    if (!window.confirm("Claude の長期トークンを削除しますか？これから起動するセッションは通常のログインで動きます。")) return;
    setBusy(true);
    setError(null);
    try {
      const s = await api.deleteClaudeToken();
      setToken(s.token ?? null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const btn = "text-xs px-3 py-1.5 rounded-md border disabled:opacity-50";
  return (
    <div className="rounded-md border border-gray-200 px-3 py-2 space-y-2">
      <div className="text-xs font-semibold text-gray-500">長期トークン（claude setup-token）</div>
      {token === null ? (
        <div className="text-xs text-gray-400">Loading...</div>
      ) : token.present ? (
        <div className={`text-sm ${token.expiringSoon ? "text-amber-700" : "text-gray-700"}`}>
          {token.expired ? "長期トークンの期限が切れています" : "長期トークンで動作中"}（期限 {formatDay(token.expiresAt)}）
        </div>
      ) : (
        <div className="text-sm text-gray-500">長期トークンは使っていません（通常のログインで動作中）</div>
      )}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={openClaudeToken}
          className={`${btn} border-gray-300 text-gray-700 hover:bg-gray-50`}
        >
          {token?.present ? "作り直す" : "長期トークンを作成"}
        </button>
        {token?.present && (
          <button
            type="button"
            onClick={remove}
            disabled={busy}
            className={`${btn} border-red-200 text-red-600 hover:bg-red-50`}
          >
            削除
          </button>
        )}
      </div>
      {error && <div className="text-xs text-red-600">{error}</div>}
    </div>
  );
}
