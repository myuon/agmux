import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { FileText, Pin } from "lucide-react";
import { api, type Artifact } from "../../api/client";
import { useWebSocket } from "../../hooks/useWebSocket";
import { ARTIFACT_WS_TYPES, sortArtifacts } from "../../lib/artifact";

// Compact list of HTML artifacts published by a session. Renders nothing when
// there are none. Titles are rendered as plain text.
export function ArtifactList({ sessionId }: { sessionId: string }) {
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);

  const load = useCallback(() => {
    api
      .listArtifacts(sessionId)
      .then((list) => setArtifacts(sortArtifacts(list)))
      .catch(() => {});
  }, [sessionId]);

  useEffect(() => {
    load();
  }, [load]);

  useWebSocket(
    useCallback(
      (msg: { type: string; data: unknown }) => {
        if (!ARTIFACT_WS_TYPES.includes(msg.type)) return;
        if ((msg.data as Artifact).sessionId === sessionId) load();
      },
      [sessionId, load],
    ),
  );

  if (artifacts.length === 0) return null;
  return (
    <div className="shrink-0 pt-2 flex flex-nowrap gap-2 overflow-x-auto [scrollbar-width:thin] touch-pan-x" data-testid="artifact-list">
      {artifacts.map((a) => (
        <Link
          key={a.id}
          to={`/artifacts/${a.id}`}
          className="inline-flex shrink-0 items-center gap-1 max-w-[16rem] px-2 py-1 rounded border border-gray-300 bg-white text-xs text-gray-700 hover:bg-gray-50"
        >
          {a.pinned ? (
            <Pin size={12} className="shrink-0 fill-amber-400 text-amber-500" />
          ) : (
            <FileText size={12} className="shrink-0" />
          )}
          <span className="truncate">{a.title}</span>
        </Link>
      ))}
    </div>
  );
}
