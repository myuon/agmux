import { useCallback, useEffect, useRef, useState } from "react";
import { ExternalLink, X } from "lucide-react";
import { api } from "../api/client";
import type { ClaudeAuthStatus, ClaudeLoginStatus } from "../api/client";
import { useExperimental } from "../hooks/useExperimental";
import { useWebSocket } from "../hooks/useWebSocket";
import { OPEN_CLAUDE_LOGIN_EVENT, OPEN_CLAUDE_TOKEN_EVENT, formatDay } from "../lib/claudeAuth";
import { ClaudeTokenDialog } from "./ClaudeTokenDialog";
import { Modal } from "./ui/Modal";

const ACTIVE_PHASES = new Set(["starting", "waiting_code", "verifying"]);

// Global banner + dialog for the experimental "claude_relogin" feature. Renders
// nothing (and calls no API) while the feature is off.
export function ClaudeLogin() {
  const enabled = useExperimental("claude_relogin");
  if (!enabled) return null;
  return <ClaudeLoginInner />;
}

function ClaudeLoginInner() {
  const [status, setStatus] = useState<ClaudeAuthStatus | null>(null);
  const [open, setOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  // The banner can be hidden until the state changes again.
  const [hiddenKey, setHiddenKey] = useState<string | null>(null);

  const load = useCallback(() => {
    api.getClaudeAuthStatus().then(setStatus).catch(() => {});
  }, []);

  useEffect(() => {
    load();
    const onOpen = () => {
      setOpen(true);
      load();
    };
    const onOpenToken = () => {
      setTokenOpen(true);
      load();
    };
    window.addEventListener(OPEN_CLAUDE_LOGIN_EVENT, onOpen);
    window.addEventListener(OPEN_CLAUDE_TOKEN_EVENT, onOpenToken);
    return () => {
      window.removeEventListener(OPEN_CLAUDE_LOGIN_EVENT, onOpen);
      window.removeEventListener(OPEN_CLAUDE_TOKEN_EVENT, onOpenToken);
    };
  }, [load]);

  useWebSocket(
    useCallback((msg: { type: string; data: unknown }) => {
      if (msg.type === "claude_auth") setStatus(msg.data as ClaudeAuthStatus);
      if (msg.type === "server_started") load();
    }, [load]),
  );

  if (!status)
    return (
      <>
        <ClaudeLoginDialog open={open} onClose={() => setOpen(false)} status={null} setStatus={setStatus} reload={load} />
        <ClaudeTokenDialog open={tokenOpen} onClose={() => setTokenOpen(false)} status={null} setStatus={setStatus} reload={load} />
      </>
    );

  const stopped = status.stoppedSessions.length;
  const token = status.token;
  const tokenExpiring = !!token?.present && token.expiringSoon;
  const kind: "required" | "token" | "stopped" | null = status.loginRequired
    ? "required"
    : tokenExpiring
      ? "token"
      : stopped > 0
        ? "stopped"
        : null;
  const bannerKey =
    kind === "required"
      ? `required:${status.since ?? ""}`
      : kind === "token"
        ? `token:${token?.expiresAt ?? ""}`
        : kind === "stopped"
          ? `stopped:${stopped}`
          : null;
  const showBanner = bannerKey !== null && bannerKey !== hiddenKey;
  const bannerColor =
    kind === "required" ? "bg-red-600 text-white" : kind === "token" ? "bg-amber-500 text-gray-900" : "bg-emerald-600 text-white";

  return (
    <>
      {showBanner && (
        <div
          role="alert"
          className={`fixed top-0 inset-x-0 z-40 px-3 py-2 text-sm shadow-md flex items-center gap-2 ${bannerColor}`}
        >
          <span className="flex-1 min-w-0 leading-snug">
            {kind === "required"
              ? "Claude のログインが切れています"
              : kind === "token"
                ? token?.expired
                  ? `Claude の長期トークンの期限が切れています（期限 ${formatDay(token?.expiresAt)}）`
                  : `Claude の長期トークンの期限が近づいています（期限 ${formatDay(token?.expiresAt)}）`
                : `再ログインしました。止まったセッションが ${stopped} 件あります`}
          </span>
          <button
            onClick={() => (kind === "token" ? setTokenOpen(true) : setOpen(true))}
            className="shrink-0 rounded-md bg-white px-3 py-1.5 text-sm font-medium text-gray-900 hover:bg-gray-100"
          >
            {kind === "required" ? "再ログイン" : kind === "token" ? "作り直す" : "確認"}
          </button>
          <button
            onClick={() => setHiddenKey(bannerKey)}
            className="shrink-0 p-1.5 rounded hover:bg-white/20"
            aria-label="バナーを閉じる"
            title="閉じる"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}
      <ClaudeLoginDialog open={open} onClose={() => setOpen(false)} status={status} setStatus={setStatus} reload={load} />
      <ClaudeTokenDialog open={tokenOpen} onClose={() => setTokenOpen(false)} status={status} setStatus={setStatus} reload={load} />
    </>
  );
}

function ClaudeLoginDialog({
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

  const login = status?.login ?? { phase: "idle" as const };
  const phase = login.phase;

  // Poll while the CLI is working (the result is not pushed over WebSocket).
  useEffect(() => {
    if (!open || !(phase === "starting" || phase === "verifying")) return;
    const t = setInterval(reload, 1500);
    return () => clearInterval(t);
  }, [open, phase, reload]);

  // Refresh when the dialog opens.
  useEffect(() => {
    if (open) {
      setError(null);
      reload();
    }
  }, [open, reload]);

  const applyLogin = (l: ClaudeLoginStatus) => {
    const cur = statusRef.current;
    if (cur) setStatus({ ...cur, login: l });
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
    applyLogin(await api.startClaudeLogin());
  });
  const submit = () => run(async () => {
    const c = code.trim();
    if (!c) return;
    const l = await api.submitClaudeLoginCode(c);
    setCode("");
    applyLogin(l);
  });
  const cancel = () => run(async () => {
    applyLogin(await api.cancelClaudeLogin());
  });
  const resume = (id: string) => run(async () => {
    setStatus(await api.resumeClaudeSession(id));
  });
  const dismiss = (id: string) => run(async () => {
    setStatus(await api.dismissClaudeSession(id));
  });
  const copyUrl = async () => {
    if (!login.url) return;
    try {
      await navigator.clipboard.writeText(login.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (e.g. plain http): the link is still there.
    }
  };

  const active = ACTIVE_PHASES.has(phase);
  const required = status?.loginRequired ?? false;
  const btn = "min-h-11 px-4 rounded-lg text-sm font-medium disabled:opacity-50";

  return (
    <Modal open={open} onClose={onClose} title="Claude の再ログイン">
      <div className="space-y-4 text-sm text-gray-700">
        <div className={`rounded-lg px-3 py-2 ${required ? "bg-red-50 text-red-800" : "bg-gray-50 text-gray-700"}`}>
          {required ? (
            <>
              <div className="font-medium">Claude のログインが切れています</div>
              {status?.reason && <div className="mt-0.5 text-xs wrap-anywhere">{status.reason}</div>}
            </>
          ) : (
            <div>Claude にはログインしています。</div>
          )}
        </div>

        {(phase === "idle" || phase === "failed" || phase === "succeeded") && (
          <div className="space-y-2">
            {phase === "failed" && login.message && (
              <div className="rounded-lg bg-yellow-50 px-3 py-2 text-yellow-800 wrap-anywhere">{login.message}</div>
            )}
            {phase === "succeeded" && (
              <div className="rounded-lg bg-emerald-50 px-3 py-2 text-emerald-800">ログインしました。</div>
            )}
            <p className="text-xs text-gray-500">
              ホストで <code>claude auth login</code> を起動し、承認用の URL を表示します。
            </p>
            <button onClick={start} disabled={busy} className={`${btn} w-full bg-blue-600 text-white hover:bg-blue-700`}>
              {phase === "idle" ? "ログインを始める" : "もう一度ログインする"}
            </button>
          </div>
        )}

        {phase === "starting" && <div className="text-gray-500">claude auth login を起動しています…</div>}

        {phase === "waiting_code" && login.url && (
          <div className="space-y-3">
            <ol className="list-decimal pl-5 space-y-1 text-sm">
              <li>下のボタンで claude.ai を開き、承認します</li>
              <li>表示されたコードをコピーして、ここに貼ります</li>
            </ol>
            <div className="flex gap-2">
              <a
                href={login.url}
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
              <label htmlFor="claude-login-code" className="text-xs text-gray-500">コード</label>
              <div className="flex gap-2">
                <input
                  id="claude-login-code"
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
            {login.expiresAt && (
              <p className="text-xs text-gray-400">{new Date(login.expiresAt).toLocaleTimeString()} までに完了してください</p>
            )}
          </div>
        )}

        {phase === "verifying" && <div className="text-gray-500">ログインを確認しています…</div>}

        {active && (
          <button onClick={cancel} disabled={busy} className="text-xs text-gray-500 underline">
            中止する
          </button>
        )}

        {error && <div className="text-sm text-red-600 wrap-anywhere">{error}</div>}

        {status && status.stoppedSessions.length > 0 && (
          <div className="space-y-2 border-t border-gray-100 pt-3">
            <div className="text-xs font-semibold text-gray-500">ログイン切れで止まったセッション</div>
            {required && <p className="text-xs text-gray-400">再ログインすると再開できます。</p>}
            <ul className="space-y-2">
              {status.stoppedSessions.map((s) => (
                <li key={s.id} className="flex items-center gap-2">
                  <a href={`/sessions/${s.id}`} className="flex-1 min-w-0 truncate text-blue-700 hover:underline">
                    {s.name || s.id}
                  </a>
                  <button
                    onClick={() => resume(s.id)}
                    disabled={busy || required}
                    className="min-h-9 px-3 rounded-md bg-blue-600 text-white text-xs disabled:opacity-40"
                  >
                    再開
                  </button>
                  <button
                    onClick={() => dismiss(s.id)}
                    disabled={busy}
                    className="min-h-9 px-2 rounded-md border border-gray-300 text-xs text-gray-600"
                  >
                    外す
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </Modal>
  );
}
