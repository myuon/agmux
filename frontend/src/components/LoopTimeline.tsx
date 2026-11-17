import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { AutomationRunReport, AutomationTimelineEvent } from "../api/client";
import { Section } from "./ui/Section";
import { AlertBanner } from "./ui/AlertBanner";
import { StreamOutputView } from "./session/StreamOutputView";
import { formatDuration, formatNum } from "./LoopDashboard";

const RUN_STATUS_STYLES: Record<AutomationRunReport["status"], string> = {
  success: "bg-green-50 border-green-300 text-green-700",
  skipped: "bg-yellow-50 border-yellow-300 text-yellow-700",
  error: "bg-red-50 border-red-300 text-red-700",
};

// Number of stream lines fetched on expand / per "もっと見る".
const CONVERSATION_TAIL = 60;
const CONVERSATION_PAGE = 100;

function eventKey(e: AutomationTimelineEvent): string {
  return [e.kind, e.at, e.run?.id ?? "", e.feeder?.id ?? "", e.task?.id ?? ""].join(":");
}

/** Conversation of a run's session: tail N lines, older ones on demand. */
function RunConversation({ sessionId }: { sessionId: string }) {
  const [lines, setLines] = useState<unknown[]>([]);
  const [start, setStart] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .getStreamOutput(sessionId, CONVERSATION_TAIL)
      .then((r) => {
        if (cancelled) return;
        setLines(r.lines);
        setStart(r.start ?? Math.max(0, r.total - r.lines.length));
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : "会話の取得に失敗しました");
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  const loadMore = async () => {
    setLoadingMore(true);
    try {
      const r = await api.getStreamOutputBefore(sessionId, start, CONVERSATION_PAGE);
      setLines((prev) => [...r.lines, ...prev]);
      setStart(r.start);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "会話の取得に失敗しました");
    } finally {
      setLoadingMore(false);
    }
  };

  if (!loaded) return <div className="text-xs text-gray-400">会話を読み込み中...</div>;
  return (
    <div className="space-y-1.5 min-w-0">
      {error && <AlertBanner variant="error">{error}</AlertBanner>}
      {start > 0 && (
        <button
          onClick={loadMore}
          disabled={loadingMore}
          className="text-xs text-blue-600 hover:underline disabled:opacity-50"
        >
          {loadingMore ? "読み込み中..." : `もっと見る（さらに前の ${start} 件）`}
        </button>
      )}
      {lines.length === 0 ? (
        <div className="text-xs text-gray-400">会話の記録がありません</div>
      ) : (
        <div className="h-[28rem] flex flex-col border border-gray-200 rounded p-2 min-w-0">
          <StreamOutputView lines={lines} sessionId={sessionId} className="flex-1 min-h-0" />
        </div>
      )}
    </div>
  );
}

function RunEvent({ ev }: { ev: AutomationTimelineEvent }) {
  const [open, setOpen] = useState(false);
  const r = ev.run!;
  const running = !!r.sessionId && !r.stopped && r.status === "success";
  return (
    <div className="space-y-1.5 min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={`px-2 py-0.5 text-xs font-medium rounded-full border ${RUN_STATUS_STYLES[r.status]}`}
        >
          {r.status}
        </span>
        {ev.feeder ? (
          <Link
            to={`/automations/${ev.feeder.id}`}
            className="text-xs px-1.5 py-0.5 rounded bg-purple-50 border border-purple-200 text-purple-700 break-all"
          >
            供給元: {ev.feeder.name}
          </Link>
        ) : (
          <span className="text-xs text-gray-500">実行</span>
        )}
        {running && <span className="text-xs text-blue-600">実行中</span>}
        <span className="text-xs text-gray-500">{new Date(r.firedAt).toLocaleString()}</span>
      </div>
      <div className="text-xs text-gray-500">
        所要 {formatDuration(r.durationSeconds)} / コスト ${(r.cost || 0).toFixed(2)}
      </div>
      {(ev.runTasks?.length ?? 0) > 0 && (
        <div className="text-xs text-gray-700 break-words">
          タスク: {ev.runTasks!.map((t) => t.title).join(", ")}
        </div>
      )}
      {(ev.addedTasks?.length ?? 0) > 0 && (
        <div className="text-xs text-gray-700 break-words">
          積んだタスク: {ev.addedTasks!.join(", ")}
        </div>
      )}
      {r.message && <div className="text-xs text-gray-500 break-all">{r.message}</div>}
      {r.report && (
        <div className="space-y-1">
          {r.report.summary && (
            <div className="text-sm text-gray-800 whitespace-pre-wrap break-words">
              {r.report.summary}
            </div>
          )}
          {Object.keys(r.report.metrics).length > 0 && (
            <div className="flex flex-wrap gap-1">
              {Object.entries(r.report.metrics).map(([k, v]) => (
                <span key={k} className="px-1.5 py-0.5 text-xs bg-gray-100 rounded font-mono">
                  {k}: {formatNum(v)}
                </span>
              ))}
            </div>
          )}
          {r.report.links.length > 0 && (
            <div className="flex flex-wrap gap-x-3 text-xs">
              {r.report.links.map((l, i) => (
                <a
                  key={i}
                  href={l.url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-blue-600 hover:underline break-all"
                >
                  {l.title || l.url}
                </a>
              ))}
            </div>
          )}
        </div>
      )}
      {r.sessionId && (
        <div className="space-y-1.5">
          <div className="flex items-center gap-3">
            <button
              onClick={() => setOpen((v) => !v)}
              className="text-xs text-blue-600 hover:underline"
            >
              {open ? "▼ 会話を畳む" : "▶ 会話を見る"}
            </button>
            <Link
              to={`/sessions/${r.sessionId}`}
              className="text-xs text-gray-500 hover:underline ml-auto shrink-0"
            >
              セッションを開く
            </Link>
          </div>
          {open && <RunConversation sessionId={r.sessionId} />}
        </div>
      )}
    </div>
  );
}

function TaskEvent({ ev }: { ev: AutomationTimelineEvent }) {
  const t = ev.task!;
  if (ev.kind === "task_added") {
    return (
      <div className="space-y-0.5 min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="px-2 py-0.5 text-xs font-medium rounded-full border bg-gray-50 border-gray-300 text-gray-600">
            タスク追加
          </span>
          <span className="text-xs text-gray-500">{new Date(ev.at).toLocaleString()}</span>
        </div>
        <div className="text-sm text-gray-800 break-words">{t.title}</div>
        <div className="text-xs text-gray-500 break-all">
          追加者: {t.createdBy === "agent" ? "agent" : "user"}
          {ev.source && (
            <>
              {" "}
              （
              <Link to={`/automations/${ev.source.id}`} className="text-blue-600 hover:underline">
                {ev.source.name}
              </Link>
              ）
            </>
          )}
        </div>
      </div>
    );
  }
  const failed = t.status === "failed";
  return (
    <div className="space-y-0.5 min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={`px-2 py-0.5 text-xs font-medium rounded-full border ${
            failed
              ? "bg-red-50 border-red-300 text-red-700"
              : "bg-green-50 border-green-300 text-green-700"
          }`}
        >
          タスク{failed ? "失敗" : "完了"}
        </span>
        <span className="text-xs text-gray-500">{new Date(ev.at).toLocaleString()}</span>
      </div>
      <div className="text-sm text-gray-800 break-words">{t.title}</div>
      {t.result && (
        <div className="text-xs text-gray-600 whitespace-pre-wrap break-words">{t.result}</div>
      )}
    </div>
  );
}

function DisabledEvent({ ev }: { ev: AutomationTimelineEvent }) {
  return (
    <div className="space-y-0.5 min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="px-2 py-0.5 text-xs font-medium rounded-full border bg-orange-50 border-orange-300 text-orange-700">
          自動停止
        </span>
        <span className="text-xs text-gray-500">{new Date(ev.at).toLocaleString()}</span>
      </div>
      <div className="text-sm text-gray-800 break-words">{ev.message}</div>
    </div>
  );
}

/**
 * Chronological timeline (newest first) of a loop (experimental "loops"):
 * runs, task additions / completions and watchdog stops. Run conversations are
 * fetched when the run is expanded.
 */
export function LoopTimeline({ automationId }: { automationId: string }) {
  const [head, setHead] = useState<AutomationTimelineEvent[]>([]);
  const [older, setOlder] = useState<AutomationTimelineEvent[]>([]);
  const [nextBefore, setNextBefore] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Once older pages were requested the cursor belongs to the paging state.
  const pagedRef = useRef(false);

  const reload = useCallback(async () => {
    try {
      const tl = await api.getAutomationTimeline(automationId);
      setHead(tl.events);
      // Only the first load decides the cursor; later refreshes keep paging state.
      if (!pagedRef.current) setNextBefore(tl.nextBefore);
      setError(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "タイムラインの取得に失敗しました");
    } finally {
      setLoaded(true);
    }
  }, [automationId]);

  useEffect(() => {
    reload();
    const t = setInterval(reload, 10000);
    return () => clearInterval(t);
  }, [reload]);

  const loadMore = async () => {
    setLoadingMore(true);
    pagedRef.current = true;
    try {
      const tl = await api.getAutomationTimeline(automationId, nextBefore);
      setOlder((prev) => [...prev, ...tl.events]);
      setNextBefore(tl.nextBefore);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "タイムラインの取得に失敗しました");
    } finally {
      setLoadingMore(false);
    }
  };

  const seen = new Set<string>();
  const events = [...head, ...older].filter((e) => {
    const k = eventKey(e);
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });

  return (
    <Section title="タイムライン">
      {error && <AlertBanner variant="error">{error}</AlertBanner>}
      {!loaded ? (
        <div className="text-sm text-gray-400">読み込み中...</div>
      ) : events.length === 0 ? (
        <div className="text-sm text-gray-400">まだイベントがありません</div>
      ) : (
        <ol className="border-l-2 border-gray-200 ml-1 space-y-4 min-w-0">
          {events.map((ev) => (
            <li key={eventKey(ev)} className="pl-3 relative min-w-0">
              <span className="absolute -left-[5px] top-1.5 w-2 h-2 rounded-full bg-gray-400" />
              {ev.kind === "run" ? (
                <RunEvent ev={ev} />
              ) : ev.kind === "disabled" ? (
                <DisabledEvent ev={ev} />
              ) : (
                <TaskEvent ev={ev} />
              )}
            </li>
          ))}
        </ol>
      )}
      {nextBefore && (
        <button
          onClick={loadMore}
          disabled={loadingMore}
          className="text-sm text-blue-600 hover:underline disabled:opacity-50"
        >
          {loadingMore ? "読み込み中..." : "さらに古いイベントを見る"}
        </button>
      )}
    </Section>
  );
}
