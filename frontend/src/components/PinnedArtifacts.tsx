import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Pin } from "lucide-react";
import { api, type Artifact } from "../api/client";
import { useWebSocket } from "../hooks/useWebSocket";
import { ARTIFACT_WS_TYPES, sortArtifacts } from "../lib/artifact";

// Home-page shortcuts to pinned artifacts (one tap to open). Renders nothing
// when no artifact is pinned. Titles are rendered as plain text.
export function PinnedArtifacts() {
  const [pinned, setPinned] = useState<Artifact[]>([]);

  const load = useCallback(() => {
    api
      .listAllArtifacts()
      .then((list) => setPinned(sortArtifacts(list).filter((a) => a.pinned)))
      .catch(() => {});
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  useWebSocket(
    useCallback(
      (msg: { type: string; data: unknown }) => {
        if (ARTIFACT_WS_TYPES.includes(msg.type)) load();
      },
      [load],
    ),
  );

  if (pinned.length === 0) return null;
  return (
    <div
      className="shrink-0 flex flex-nowrap gap-2 overflow-x-auto px-3 py-2 bg-white border-b border-gray-200 [scrollbar-width:thin] touch-pan-x"
      data-testid="pinned-artifacts"
    >
      {pinned.map((a) => (
        <Link
          key={a.id}
          to={`/artifacts/${a.id}`}
          className="inline-flex shrink-0 items-center gap-1.5 max-w-[16rem] px-3 py-1.5 rounded-full border border-amber-300 bg-amber-50 text-sm text-gray-800 hover:bg-amber-100"
        >
          <Pin size={14} className="shrink-0 fill-amber-400 text-amber-500" />
          <span className="truncate">{a.title}</span>
        </Link>
      ))}
    </div>
  );
}
