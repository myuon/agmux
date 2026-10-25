import { useContext, useEffect, useMemo, useState } from "react";
import type { StreamEntry } from "../../models/stream";
import { filterSubagentLines, mergeStreamEntries } from "../../models/stream";
import { api } from "../../api/client";
import { StreamDisplayItemView, StreamDisplayItemsView } from "./StreamDisplayItemView";
import { StreamLinesContext } from "./StreamLinesContext";


const REFRESH_DEBOUNCE_MS = 1000;

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-50", label: "Prompt", text: "text-blue-700" },
  assistant: { bg: "bg-gray-50", label: "Subagent", text: "text-indigo-700" },
};

function toEntries(lines: unknown[]): StreamEntry[] {
  return lines
    .map((line) => line as StreamEntry)
    .filter((e) => e && (e.type === "user" || e.type === "assistant" || e.type === "system" || e.type === "result"));
}

// Renders the conversation of a subagent (lines whose parent_tool_use_id is
// parentToolUseId) with the same item components as the main timeline.
export function SubagentThreadView({ sessionId, parentToolUseId, live, sessionActive, toolProgress, emptyFallback }: {
  sessionId?: string;
  parentToolUseId: string;
  // Whether the subagent may still produce lines (re-fetch on parent stream updates)
  live?: boolean;
  sessionActive?: boolean;
  toolProgress?: Record<string, number>;
  // Rendered instead of the thread when it has no lines (e.g. background agents)
  emptyFallback?: React.ReactNode;
}) {
  const parentLines = useContext(StreamLinesContext);
  const [fetched, setFetched] = useState<unknown[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  // Debounced refresh trigger when the parent session's live stream changes
  useEffect(() => {
    if (!sessionId || !live || parentLines === null) return;
    const t = setTimeout(() => setRefreshKey((k) => k + 1), REFRESH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [sessionId, live, parentLines]);

  useEffect(() => {
    if (!sessionId) return;
    let cancelled = false;
    api.getSubagentStream(sessionId, parentToolUseId)
      .then((resp) => {
        if (cancelled) return;
        setFetched(resp.lines);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => { cancelled = true; };
  }, [sessionId, parentToolUseId, refreshKey]);

  const lines = useMemo(() => {
    if (sessionId) return fetched;
    return parentLines ? filterSubagentLines(parentLines, parentToolUseId) : [];
  }, [sessionId, fetched, parentLines, parentToolUseId]);

  const groups = useMemo(
    () => (lines ? mergeStreamEntries(toEntries(lines), undefined, undefined, parentToolUseId) : []),
    [lines, parentToolUseId],
  );

  if (lines === null) {
    return error
      ? <p className="text-xs text-red-500">Failed to load subagent thread: {error}</p>
      : <p className="text-xs text-gray-400">Loading subagent thread...</p>;
  }
  if (lines.length === 0) {
    return <>{emptyFallback ?? <p className="text-xs text-gray-400">No subagent activity recorded</p>}</>;
  }

  return (
    <div className="space-y-2">
      {groups.map((group, i) => {
        if (group.role === "system") {
          return (
            <div key={i}>
              {group.items.map((item, j) => (
                <StreamDisplayItemView key={j} item={item} sessionId={sessionId} sessionActive={sessionActive} toolProgress={toolProgress} />
              ))}
            </div>
          );
        }
        const style = roleStyles[group.role] || roleStyles.assistant;
        return (
          <div key={i} className={`rounded-lg p-2 ${style.bg}`}>
            <div className={`font-semibold text-[11px] mb-1 ${style.text}`}>{style.label}</div>
            <div className="text-gray-800 break-words text-xs space-y-2">
              <StreamDisplayItemsView items={group.items} sessionId={sessionId} sessionActive={sessionActive} toolProgress={toolProgress} />
            </div>
          </div>
        );
      })}
    </div>
  );
}
