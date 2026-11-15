import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { CheckCircle2, KeyRound, Loader2 } from "lucide-react";
import { api } from "../api/client";
import { SecretForm } from "../components/SecretForm";
import { SECRET_RESPONSE_CANCELLED, type SecretMeta, type SecretRequestInfo } from "../lib/secret";

type RequestState =
  | { kind: "none" }
  | { kind: "loading" }
  | { kind: "pending"; info: SecretRequestInfo; sessionName: string | null }
  | { kind: "gone" };

/**
 * Mobile-first page to register a secret. With `?escalation=&session=` it
 * answers an agent's request_secret (name fixed); otherwise it registers a
 * secret directly.
 */
export function SecretNewPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const escalationId = params.get("escalation") ?? "";
  const sessionId = params.get("session") ?? "";
  const isRequest = escalationId !== "" && sessionId !== "";

  const requestKey = isRequest ? `${sessionId}/${escalationId}` : "";
  const [loaded, setLoaded] = useState<{ key: string; state: RequestState } | null>(null);
  const req: RequestState = !isRequest
    ? { kind: "none" }
    : loaded && loaded.key === requestKey
      ? loaded.state
      : { kind: "loading" };
  const [saved, setSaved] = useState<SecretMeta | null>(null);
  const [cancelled, setCancelled] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);

  useEffect(() => {
    if (!requestKey) return;
    let alive = true;
    Promise.all([
      api.getPendingEscalation(sessionId),
      api.getSession(sessionId).then((s) => s.name).catch(() => null),
    ])
      .then(([r, sessionName]) => {
        if (!alive) return;
        const esc = r.escalation;
        if (esc && esc.id === escalationId && esc.secretRequest && !esc.timedOut) {
          setLoaded({ key: requestKey, state: { kind: "pending", info: esc.secretRequest, sessionName } });
        } else {
          setLoaded({ key: requestKey, state: { kind: "gone" } });
        }
      })
      .catch(() => {
        if (alive) setLoaded({ key: requestKey, state: { kind: "gone" } });
      });
    return () => {
      alive = false;
    };
  }, [requestKey, escalationId, sessionId]);

  const back = () => (isRequest ? navigate(`/sessions/${sessionId}`) : navigate(-1));

  const cancelRequest = async () => {
    setCancelError(null);
    try {
      await api.respondEscalation(sessionId, escalationId, SECRET_RESPONSE_CANCELLED);
      setCancelled(true);
    } catch (err) {
      setCancelError(err instanceof Error ? err.message : String(err));
    }
  };

  let content: React.ReactNode;
  if (saved) {
    content = (
      <div className="bg-white rounded-xl border border-gray-200 p-5 space-y-3 text-center" data-testid="secret-saved">
        <CheckCircle2 className="w-10 h-10 text-green-600 mx-auto" />
        <div className="text-base font-medium">
          <span className="font-mono">{saved.name}</span> を保存しました
        </div>
        <p className="text-xs text-gray-500">
          {isRequest ? "エージェントには保存したことだけが伝えられます。" : "エージェントは "}
          {!isRequest && <code className="font-mono">agmux secret exec --env {saved.name} -- &lt;cmd&gt;</code>}
          {!isRequest && " で利用できます。"}
        </p>
        {isRequest ? (
          <Link to={`/sessions/${sessionId}`} className="inline-block px-4 py-2 text-sm bg-blue-600 text-white rounded-lg">
            セッションに戻る
          </Link>
        ) : (
          <button onClick={() => setSaved(null)} className="px-4 py-2 text-sm border border-gray-300 rounded-lg">
            続けて追加
          </button>
        )}
      </div>
    );
  } else if (cancelled) {
    content = (
      <div className="bg-white rounded-xl border border-gray-200 p-5 space-y-3 text-center">
        <div className="text-base">依頼をキャンセルしました</div>
        <Link to={`/sessions/${sessionId}`} className="inline-block px-4 py-2 text-sm bg-blue-600 text-white rounded-lg">
          セッションに戻る
        </Link>
      </div>
    );
  } else if (req.kind === "loading") {
    content = (
      <div className="flex justify-center py-12 text-gray-400">
        <Loader2 className="w-6 h-6 animate-spin" />
      </div>
    );
  } else if (req.kind === "gone") {
    content = (
      <div className="bg-white rounded-xl border border-gray-200 p-5 space-y-3 text-center" data-testid="secret-request-gone">
        <div className="text-sm text-gray-700">この依頼はすでに完了・キャンセル・タイムアウトしています。</div>
        <div className="flex justify-center gap-2">
          <Link to={`/sessions/${sessionId}`} className="px-4 py-2 text-sm border border-gray-300 rounded-lg">
            セッションへ
          </Link>
          <Link to="/secrets/new" className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg">
            シークレットを追加
          </Link>
        </div>
      </div>
    );
  } else if (req.kind === "pending") {
    const { info, sessionName } = req;
    content = (
      <div className="space-y-3">
        <div className="bg-amber-50 border border-amber-200 rounded-xl p-3 space-y-1" data-testid="secret-request-info">
          <div className="text-xs text-amber-800">
            {sessionName ? (
              <>
                <Link to={`/sessions/${sessionId}`} className="font-medium underline">{sessionName}</Link> からの依頼
              </>
            ) : (
              "エージェントからの依頼"
            )}
          </div>
          <div className="text-sm text-gray-800 whitespace-pre-wrap break-words">{info.description}</div>
        </div>
        <div className="bg-white rounded-xl border border-gray-200 p-4">
          <SecretForm
            key={escalationId}
            initialName={info.name}
            nameReadOnly
            initialDescription={info.description}
            defaultProject={info.projectPath ?? ""}
            submit={(body) => api.resolveSecretRequest(sessionId, escalationId, body)}
            onSaved={setSaved}
            onCancel={cancelRequest}
            cancelLabel="依頼をキャンセル"
          />
          {cancelError && <div className="mt-2 text-sm text-red-700" role="alert">{cancelError}</div>}
        </div>
      </div>
    );
  } else {
    content = (
      <div className="bg-white rounded-xl border border-gray-200 p-4">
        <SecretForm submit={(body) => api.registerSecret(body)} onSaved={setSaved} onCancel={back} />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-4 py-3 flex items-center gap-3">
        <button onClick={back} className="text-gray-500 hover:text-gray-700 text-sm">
          ← Back
        </button>
        <h1 className="text-lg font-bold flex items-center gap-2">
          <KeyRound className="w-5 h-5" />
          {isRequest ? "シークレット入力依頼" : "シークレットを追加"}
        </h1>
      </header>
      <div className="max-w-md mx-auto p-3 sm:p-6" data-testid="secret-new-page">
        {content}
      </div>
    </div>
  );
}
