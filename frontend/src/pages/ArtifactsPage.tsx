import { useCallback, useState } from "react";
import { Link, useLoaderData, useNavigate } from "react-router-dom";
import { FileText, Pin } from "lucide-react";
import { api, type Artifact } from "../api/client";
import type { Session } from "../types/session";
import { useWebSocket } from "../hooks/useWebSocket";
import { ARTIFACT_WS_TYPES, sortArtifacts } from "../lib/artifact";

// ArtifactsPage lists every published HTML artifact across sessions.
export function ArtifactsPage() {
  const navigate = useNavigate();
  const { artifacts: initial, sessions } = useLoaderData<{
    artifacts: Artifact[];
    sessions: Session[];
  }>();
  const [artifacts, setArtifacts] = useState<Artifact[]>(sortArtifacts(initial));
  const togglePin = (a: Artifact) => {
    const pinned = !a.pinned;
    // Optimistic: apply immediately, reload on failure.
    setArtifacts((cur) => sortArtifacts(cur.map((x) => (x.id === a.id ? { ...x, pinned } : x))));
    api.setArtifactPinned(a.id, pinned).catch(() => {
      api.listAllArtifacts().then((l) => setArtifacts(sortArtifacts(l))).catch(() => {});
    });
  };
  const names = new Map(sessions.map((s) => [s.id, s.name]));

  useWebSocket(
    useCallback((msg: { type: string; data: unknown }) => {
      if (ARTIFACT_WS_TYPES.includes(msg.type)) {
        api.listAllArtifacts().then((l) => setArtifacts(sortArtifacts(l))).catch(() => {});
      }
    }, []),
  );

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-4 sm:px-6 py-3 flex items-center gap-4">
        <button onClick={() => navigate("/")} className="text-gray-500 hover:text-gray-700 text-sm">
          ← Back
        </button>
        <h1 className="text-lg font-bold">Artifacts</h1>
      </header>
      <div className="max-w-2xl mx-auto p-3 sm:p-6 space-y-2" data-testid="artifacts-page">
        {artifacts.length === 0 ? (
          <div className="text-center text-sm text-gray-400 py-12">Artifact はまだありません</div>
        ) : (
          artifacts.map((a) => (
            <div key={a.id} className="bg-white rounded-lg border border-gray-200 shadow-sm p-3 min-w-0">
              <div className="flex items-center gap-2 min-w-0">
                <Link
                  to={`/artifacts/${a.id}`}
                  className="flex flex-1 items-center gap-2 text-sm font-medium text-blue-700 hover:underline min-w-0"
                >
                  {a.pinned ? (
                    <Pin size={14} className="shrink-0 fill-amber-400 text-amber-500" data-testid="artifact-pinned-mark" />
                  ) : (
                    <FileText size={14} className="shrink-0" />
                  )}
                  <span className="truncate">{a.title}</span>
                </Link>
                <button
                  type="button"
                  onClick={() => togglePin(a)}
                  aria-pressed={!!a.pinned}
                  aria-label={a.pinned ? "ピン留めを解除" : "ピン留めする"}
                  title={a.pinned ? "ピン留めを解除" : "ピン留めする"}
                  data-testid="artifact-pin-toggle"
                  className={`shrink-0 p-1.5 rounded hover:bg-gray-100 ${a.pinned ? "text-amber-500" : "text-gray-400"}`}
                >
                  <Pin size={16} className={a.pinned ? "fill-amber-400" : ""} />
                </button>
              </div>
              <div className="mt-1 flex items-center gap-2 text-xs text-gray-500 min-w-0">
                <Link to={`/sessions/${a.sessionId}`} className="truncate hover:underline min-w-0">
                  {names.get(a.sessionId) || a.sessionId}
                </Link>
                <span className="shrink-0 ml-auto">
                  {new Date(a.updatedAt || a.createdAt).toLocaleString()}
                </span>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
