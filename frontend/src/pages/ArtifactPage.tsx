import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Trash2 } from "lucide-react";
import { api, type Artifact } from "../api/client";
import { useWebSocket } from "../hooks/useWebSocket";
import { Toast } from "../components/ui/Toast";
import { artifactRawUrl } from "../lib/artifact";

// Full-screen viewer for a published HTML artifact.
//
// Security: the HTML is loaded from the raw URL inside a sandboxed iframe
// (scripts only: no same-origin, forms, popups or top navigation). The server
// also sends a CSP `sandbox` header, so the document never runs on agmux's
// origin. Never switch this to srcdoc / innerHTML / fetch-then-inject.
export function ArtifactPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [missing, setMissing] = useState(false);
  const [updatedToast, setUpdatedToast] = useState(false);

  useEffect(() => {
    if (!id) return;
    api.getArtifact(id).then(setArtifact).catch(() => setMissing(true));
  }, [id]);

  // When this artifact is updated, take the new metadata. The new updatedAt
  // changes the iframe src, which reloads the content (scroll position resets).
  useWebSocket(
    useCallback(
      (msg: { type: string; data: unknown }) => {
        if (msg.type !== "artifact_updated") return;
        const a = msg.data as Artifact;
        if (a.id !== id) return;
        setArtifact(a);
        setUpdatedToast(true);
      },
      [id],
    ),
  );

  useEffect(() => {
    if (!updatedToast) return;
    const t = setTimeout(() => setUpdatedToast(false), 2500);
    return () => clearTimeout(t);
  }, [updatedToast]);

  const back = artifact ? `/sessions/${artifact.sessionId}` : "/";

  const remove = async () => {
    if (!id || !window.confirm("この HTML を削除しますか？")) return;
    await api.deleteArtifact(id);
    navigate(back, { replace: true });
  };

  return (
    <div className="fixed inset-0 flex flex-col bg-white">
      <div className="shrink-0 flex items-center gap-2 px-2 py-2 border-b border-gray-200">
        <Link to={back} className="p-2 text-gray-600" aria-label="戻る">
          <ArrowLeft size={20} />
        </Link>
        <h1 className="flex-1 min-w-0 truncate text-sm font-medium text-gray-900">
          {missing ? "見つかりません" : (artifact?.title ?? "")}
        </h1>
        {artifact && (
          <button onClick={remove} className="p-2 text-gray-500 hover:text-red-600" aria-label="削除">
            <Trash2 size={18} />
          </button>
        )}
      </div>
      {updatedToast && <Toast message="更新しました" onClose={() => setUpdatedToast(false)} />}
      {id && !missing && artifact && (
        <iframe
          title={artifact.title}
          src={artifactRawUrl(id, artifact?.updatedAt)}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          className="flex-1 w-full min-h-0 border-0 bg-white"
        />
      )}
    </div>
  );
}
