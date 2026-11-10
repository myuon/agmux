import { useCallback, useState } from "react";
import { Link, useLoaderData, useNavigate } from "react-router-dom";
import { FileText } from "lucide-react";
import { api, type Artifact } from "../api/client";
import type { Session } from "../types/session";
import { useWebSocket } from "../hooks/useWebSocket";

function sortArtifacts(list: Artifact[]): Artifact[] {
  return [...list].sort((a, b) =>
    Date.parse(b.updatedAt || b.createdAt) - Date.parse(a.updatedAt || a.createdAt),
  );
}

// ArtifactsPage lists every published HTML artifact across sessions.
export function ArtifactsPage() {
  const navigate = useNavigate();
  const { artifacts: initial, sessions } = useLoaderData<{
    artifacts: Artifact[];
    sessions: Session[];
  }>();
  const [artifacts, setArtifacts] = useState<Artifact[]>(sortArtifacts(initial));
  const names = new Map(sessions.map((s) => [s.id, s.name]));

  useWebSocket(
    useCallback((msg: { type: string; data: unknown }) => {
      if (
        msg.type === "artifact_created" ||
        msg.type === "artifact_updated" ||
        msg.type === "artifact_deleted"
      ) {
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
              <Link
                to={`/artifacts/${a.id}`}
                className="flex items-center gap-2 text-sm font-medium text-blue-700 hover:underline min-w-0"
              >
                <FileText size={14} className="shrink-0" />
                <span className="truncate">{a.title}</span>
              </Link>
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
