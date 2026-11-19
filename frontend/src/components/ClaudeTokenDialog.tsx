import { useEffect, useRef, useState } from "react";
import { ExternalLink } from "lucide-react";
import { api } from "../api/client";
import type { ClaudeAuthStatus, ClaudeLoginStatus } from "../api/client";
import { formatDay } from "../lib/claudeAuth";
import { Modal } from "./ui/Modal";

const ACTIVE_PHASES = new Set(["starting", "waiting_code", "verifying"]);

// Dialog that creates the long-lived token with `claude setup-token` on the
// host (experimental "claude_relogin"). The token itself never reaches the
// browser: the server reads it from the CLI and stores it as a secret.
export function ClaudeTokenDialog({
  open,
  onClose,
  status,
  setStatus,
  reload,
}: {
  open: boolean;
  onClose: () => void;
  status: ClaudeAuthStatus | null;
  setStatus: (s: ClaudeAuthStatus) => void;
  reload: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [copied, setCopied] = useState(false);
  const statusRef = useRef(status);
  statusRef.current = status;

  const token = status?.token;
  const flow = token?.flow ?? { phase: "idle" as const };
  const phase = flow.phase;

  // Poll while the CLI is working.
  useEffect(() => {
    if (!open || !(phase === "starting" || phase === "verifying")) return;
    const t = setInterval(reload, 1500);
    return () => clearInterval(t);
  }, [open, phase, reload]);

  useEffect(() => {
    if (open) {
      setError(null);
      reload();
    }
  }, [open, reload]);

  const applyFlow = (f: ClaudeLoginStatus) => {
    const cur = statusRef.current;
    if (cur?.token) setStatus({ ...cur, token: { ...cur.token, flow: f } });
    reload();
  };

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
      reload();
    } finally {
      setBusy(false);
    }
  };

  const start = () => run(async () => {
    setCode("");
    applyFlow(await api.startClaudeToken());
  });
  const submit = () => run(async () => {
    const c = code.trim();
    if (!c) return;
    const f = await api.submitClaudeTokenCode(c);
    setCode("");
    applyFlow(f);
  });
  const cancel = () => run(async () => {
    applyFlow(await api.cancelClaudeToken());
  });
  const copyUrl = async () => {
    if (!flow.url) return;
    try {
      await navigator.clipboard.writeText(flow.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (e.g. plain http): the link is still there.
    }
  };

  const active = ACTIVE_PHASES.has(phase);
  const btn = "min-h-11 px-4 rounded-lg text-sm font-medium disabled:opacity-50";

  return (
    <Modal open={open} onClose={onClose} title="Claude の長期トークン">
      <div className="space-y-4 text-sm text-gray-700">
        <div
          className={`rounded-lg px-3 py-2 ${
            token?.present && token.expiringSoon ? "bg-amber-50 text-amber-900" : "bg-gray-50 text-gray-700"
          }`}
        >
          {token?.present ? (
            <>
              <div className="font-medium">
                {token.expired ? "長期トークンの期限が切れています" : "長期トークンで動作中"}（期限 {formatDay(token.expiresAt)}）
              </div>
              <div className="mt-0.5 text-xs">作成日 {formatDay(token.createdAt)}</div>
            </>
          ) : (
            <div>長期トークンはまだありません。</div>
          )}
        </div>

        {(phase === "idle" || phase === "failed" || phase === "succeeded") && (
          <div className="space-y-2">
            {phase === "failed" && flow.message && (
              <div className="rounded-lg bg-yellow-50 px-3 py-2 text-yellow-800 wrap-anywhere">{flow.message}</div>
            )}
            {phase === "succeeded" && (
              <div className="rounded-lg bg-emerald-50 px-3 py-2 text-emerald-800">
                長期トークンを作成して保存しました。これから起動するセッションで使われます。
              </div>
            )}
            <p className="text-xs text-gray-500">
              ホストで <code>claude setup-token</code> を起動し、承認用の URL を表示します。トークン（有効期限1年）は agmux
              のシークレット <code>CLAUDE_CODE_OAUTH_TOKEN</code> に保存され、画面には表示されません。
            </p>
            <button onClick={start} disabled={busy} className={`${btn} w-full bg-blue-600 text-white hover:bg-blue-700`}>
              {token?.present ? "作り直す" : "長期トークンを作成"}
            </button>
          </div>
        )}

        {phase === "starting" && <div className="text-gray-500">claude setup-token を起動しています…</div>}

        {phase === "waiting_code" && flow.url && (
          <div className="space-y-3">
            <ol className="list-decimal pl-5 space-y-1 text-sm">
              <li>下のボタンで claude.ai を開き、承認します</li>
              <li>表示されたコードをコピーして、ここに貼ります</li>
            </ol>
            <div className="flex gap-2">
              <a
                href={flow.url}
                target="_blank"
                rel="noopener noreferrer"
                className={`${btn} flex-1 inline-flex items-center justify-center gap-1.5 bg-blue-600 text-white hover:bg-blue-700`}
              >
                <ExternalLink className="h-4 w-4" /> claude.ai で承認する
              </a>
              <button onClick={copyUrl} className={`${btn} border border-gray-300 hover:bg-gray-50`}>
                {copied ? "コピー済み" : "URL をコピー"}
              </button>
            </div>
            <div className="space-y-1">
              <label htmlFor="claude-token-code" className="text-xs text-gray-500">コード</label>
              <div className="flex gap-2">
                <input
                  id="claude-token-code"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.nativeEvent.isComposing) submit();
                  }}
                  autoComplete="off"
                  autoCapitalize="off"
                  spellCheck={false}
                  placeholder="ここにコードを貼る"
                  className="flex-1 min-w-0 min-h-11 rounded-lg border border-gray-300 px-3 font-mono text-base"
                />
                <button onClick={submit} disabled={busy || !code.trim()} className={`${btn} bg-gray-900 text-white hover:bg-gray-800`}>
                  送信
                </button>
              </div>
            </div>
            {flow.expiresAt && (
              <p className="text-xs text-gray-400">{new Date(flow.expiresAt).toLocaleTimeString()} までに完了してください</p>
            )}
          </div>
        )}

        {phase === "verifying" && <div className="text-gray-500">トークンを受け取っています…</div>}

        {active && (
          <button onClick={cancel} disabled={busy} className="text-xs text-gray-500 underline">
            中止する
          </button>
        )}

        {error && <div className="text-sm text-red-600 wrap-anywhere">{error}</div>}
      </div>
    </Modal>
  );
}
