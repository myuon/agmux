import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { FileText } from "lucide-react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api } from "../../api/client";
import { parseArtifactUrl } from "../../lib/artifact";

// Block-style link to an artifact. Built from spans (display:flex) so it is
// valid DOM even when rendered inside a <p>.
function ArtifactLinkBlock({ id, fallback }: { id: string; fallback: string }) {
  const [title, setTitle] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    api
      .getArtifact(id)
      .then((a) => {
        if (!cancelled) setTitle(a.title);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [id]);
  return (
    <Link
      to={`/artifacts/${id}`}
      data-testid="artifact-link-block"
      className="my-1 flex w-full items-center gap-2 rounded border border-gray-300 bg-white px-3 py-2 text-xs text-gray-700 no-underline hover:bg-gray-50"
    >
      <FileText size={14} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{title || fallback || "アーティファクト"}</span>
      <span className="shrink-0 text-blue-600">開く</span>
    </Link>
  );
}

function nodeText(children: React.ReactNode): string {
  return typeof children === "string" ? children : Array.isArray(children) ? children.filter((c) => typeof c === "string").join("") : "";
}

export function MarkdownText({ text }: { text: string }) {
  return (
    <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a({ href, children }) {
            const id = parseArtifactUrl(href);
            if (id) {
              const label = nodeText(children);
              return <ArtifactLinkBlock id={id} fallback={label === href ? "" : label} />;
            }
            return (
              <a href={href}>
                {children}
              </a>
            );
          },
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}
