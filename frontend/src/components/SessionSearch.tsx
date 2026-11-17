import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { SessionSearchResult } from "../api/client";

function formatDate(s: string): string {
  const d = new Date(s);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/**
 * Cross-session full-text search (experimental "session_search").
 * While a query is entered, results replace `children` (the session list).
 */
export function SessionSearch({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SessionSearchResult[] | null>(null);
  const [indexing, setIndexing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const trimmed = query.trim();

  useEffect(() => {
    if (!trimmed) return;
    let cancelled = false;
    const t = setTimeout(() => {
      setLoading(true);
      api
        .searchSessions(trimmed)
        .then((r) => {
          if (cancelled) return;
          setResults(r.results);
          setIndexing(r.indexing);
          setError(null);
        })
        .catch((e: unknown) => {
          if (!cancelled) setError(e instanceof Error ? e.message : String(e));
        })
        .finally(() => {
          if (!cancelled) setLoading(false);
        });
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [trimmed]);

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="全セッションの会話を検索"
          aria-label="全セッションの会話を検索"
          className="w-full bg-white border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
        />
      </div>
      {!trimmed ? (
        children
      ) : (
        <div className="flex flex-col gap-1.5">
          {error && <div className="text-sm text-red-500 px-1">{error}</div>}
          {indexing && (
            <div className="text-xs text-gray-500 px-1">索引を作成中のため、結果が不完全な可能性があります</div>
          )}
          {results !== null && results.length === 0 && !loading && !error && (
            <div className="text-sm text-gray-400 px-1 py-4 text-center">一致する会話はありません</div>
          )}
          {loading && results === null && <div className="text-sm text-gray-400 px-1">検索中...</div>}
          {results?.map((r, i) => (
            <button
              key={`${r.sessionId}-${i}`}
              onClick={() => navigate(`/sessions/${r.sessionId}`)}
              className="text-left bg-white border border-gray-200 rounded-lg px-3 py-2 hover:border-blue-300 transition-colors"
            >
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-sm font-medium text-gray-800 truncate">{r.sessionName || r.sessionId}</span>
                <span className="text-xs text-gray-400 shrink-0">{formatDate(r.updatedAt)}</span>
              </div>
              <div className="text-xs text-gray-600 mt-1 line-clamp-3 break-words">
                <span className={r.role === "user" ? "text-blue-600" : "text-gray-400"}>
                  {r.role === "user" ? "あなた: " : "agent: "}
                </span>
                {r.snippet}
              </div>
              {r.projectPath && <div className="text-[11px] text-gray-400 mt-1 truncate">{r.projectPath}</div>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
