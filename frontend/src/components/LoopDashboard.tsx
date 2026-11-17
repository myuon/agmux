import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { AutomationRunReport, AutomationTask } from "../api/client";
import { Section } from "./ui/Section";
import { AlertBanner } from "./ui/AlertBanner";

const RUN_STATUS_STYLES: Record<AutomationRunReport["status"], string> = {
  success: "bg-green-50 border-green-300 text-green-700",
  skipped: "bg-yellow-50 border-yellow-300 text-yellow-700",
  error: "bg-red-50 border-red-300 text-red-700",
};

function formatDuration(sec: number): string {
  if (sec <= 0) return "-";
  const s = Math.round(sec);
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}分${s % 60}秒`;
  return `${Math.floor(m / 60)}時間${m % 60}分`;
}

function formatNum(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(3).replace(/0+$/, "").replace(/\.$/, "");
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: React.ReactNode }) {
  return (
    <div className="bg-gray-50 border border-gray-200 rounded px-3 py-2 min-w-0">
      <div className="text-xs text-gray-500 break-all">{label}</div>
      <div className="text-base font-semibold text-gray-900 break-words">{value}</div>
      {sub && <div className="text-xs text-gray-500">{sub}</div>}
    </div>
  );
}

/** Small dependency-free SVG line chart; x axis is the run order. */
function Sparkline({ points }: { points: number[] }) {
  const w = 240;
  const h = 56;
  const pad = 6;
  const min = Math.min(...points);
  const max = Math.max(...points);
  const span = max - min || 1;
  const x = (i: number) =>
    points.length === 1 ? w / 2 : pad + (i * (w - pad * 2)) / (points.length - 1);
  const y = (v: number) => h - pad - ((v - min) / span) * (h - pad * 2);
  const path = points
    .map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`)
    .join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full h-14" role="img">
      <path d={path} fill="none" stroke="#2563eb" strokeWidth={1.5} />
      {points.map((v, i) => (
        <circle key={i} cx={x(i)} cy={y(v)} r={2.5} fill="#2563eb">
          <title>{`#${i + 1}: ${formatNum(v)}`}</title>
        </circle>
      ))}
    </svg>
  );
}

/**
 * Dashboard of a loop (experimental "loops"): summary, metric trends and a
 * per-run list. The caller renders it only while the feature is on.
 */
export function LoopDashboard({
  automationId,
  watchMetric,
  watchDirection,
}: {
  automationId: string;
  watchMetric?: string;
  watchDirection?: "higher" | "lower";
}) {
  const [reports, setReports] = useState<AutomationRunReport[]>([]);
  const [tasks, setTasks] = useState<AutomationTask[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      const [r, t] = await Promise.all([
        api.listAutomationReports(automationId),
        api.listAutomationTasks(automationId),
      ]);
      setReports(r);
      setTasks(t);
      setError(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "ダッシュボードの取得に失敗しました");
    } finally {
      setLoaded(true);
    }
  }, [automationId]);

  useEffect(() => {
    reload();
    const t = setInterval(reload, 10000);
    return () => clearInterval(t);
  }, [reload]);

  const total = reports.length;
  const successCount = reports.filter((r) => r.status === "success").length;
  const totalCost = reports.reduce((sum, r) => sum + (r.cost || 0), 0);
  const pending = tasks.filter((t) => t.status === "pending").length;

  // metric key -> values in chronological order (runs lacking the key are skipped)
  const metricKeys = Array.from(
    new Set(reports.flatMap((r) => Object.keys(r.report?.metrics ?? {}))),
  ).sort();
  const series = metricKeys.map((key) => ({
    key,
    values: reports
      .map((r) => r.report?.metrics?.[key])
      .filter((v): v is number => typeof v === "number"),
  }));

  const taskByRun = new Map<number, AutomationTask[]>();
  for (const t of tasks) {
    if (t.runId == null) continue;
    taskByRun.set(t.runId, [...(taskByRun.get(t.runId) ?? []), t]);
  }

  const newestFirst = [...reports].reverse();

  return (
    <>
      <Section title="ループのサマリ">
        {error && <AlertBanner variant="error">{error}</AlertBanner>}
        {!loaded ? (
          <div className="text-sm text-gray-400">読み込み中...</div>
        ) : (
          <div className="grid grid-cols-2 gap-2">
            <Stat label="総回数" value={String(total)} />
            <Stat
              label="成功率"
              value={total === 0 ? "-" : `${Math.round((successCount / total) * 100)}%`}
              sub={total > 0 ? `${successCount} / ${total}` : undefined}
            />
            <Stat label="累計コスト" value={`$${totalCost.toFixed(2)}`} />
            <Stat label="残タスク" value={`${pending} 件`} />
            {series.map(({ key, values }) => {
              const last = values[values.length - 1];
              const diff = values.length >= 2 ? last - values[values.length - 2] : null;
              // Only the watched metric is judged good/bad; other metrics stay neutral.
              const judged = key === watchMetric && !!watchDirection && diff != null && diff !== 0;
              const good = judged && (watchDirection === "higher" ? diff! > 0 : diff! < 0);
              const diffClass = !judged ? "text-gray-500" : good ? "text-green-600" : "text-red-600";
              return (
                <Stat
                  key={key}
                  label={key}
                  value={formatNum(last)}
                  sub={
                    diff == null ? (
                      "前回なし"
                    ) : (
                      <span className={diffClass}>
                        前回比 {diff > 0 ? "+" : ""}
                        {formatNum(diff)}
                      </span>
                    )
                  }
                />
              );
            })}
          </div>
        )}
      </Section>

      {series.length > 0 && (
        <Section title="指標の推移">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {series.map(({ key, values }) => (
              <div key={key} className="border border-gray-200 rounded p-2 min-w-0">
                <div className="flex justify-between text-xs text-gray-600 gap-2">
                  <span className="font-mono break-all">{key}</span>
                  <span className="shrink-0">
                    {formatNum(Math.min(...values))} - {formatNum(Math.max(...values))}
                  </span>
                </div>
                <Sparkline points={values} />
              </div>
            ))}
          </div>
        </Section>
      )}

      <Section title="回ごとの一覧">
        {loaded && newestFirst.length === 0 ? (
          <div className="text-sm text-gray-400">まだ実行されていません</div>
        ) : (
          <div className="divide-y divide-gray-100">
            {newestFirst.map((r) => {
              const runTasks = taskByRun.get(r.id) ?? [];
              const running = !!r.sessionId && !r.stopped && r.status === "success";
              return (
                <div key={r.id} className="py-3 space-y-1.5 min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span
                      className={`px-2 py-0.5 text-xs font-medium rounded-full border ${RUN_STATUS_STYLES[r.status]}`}
                    >
                      {r.status}
                    </span>
                    {running && <span className="text-xs text-blue-600">実行中</span>}
                    <span className="text-sm text-gray-800">
                      {new Date(r.firedAt).toLocaleString()}
                    </span>
                    {r.sessionId && (
                      <Link
                        to={`/sessions/${r.sessionId}`}
                        className="text-xs text-blue-600 hover:underline ml-auto shrink-0"
                      >
                        セッションを開く
                      </Link>
                    )}
                  </div>
                  <div className="text-xs text-gray-500">
                    所要 {formatDuration(r.durationSeconds)} / コスト ${(r.cost || 0).toFixed(2)}
                  </div>
                  {runTasks.length > 0 && (
                    <div className="text-xs text-gray-700 break-words">
                      タスク: {runTasks.map((t) => t.title).join(", ")}
                    </div>
                  )}
                  {r.message && <div className="text-xs text-gray-500 break-all">{r.message}</div>}
                  {r.report ? (
                    <div className="space-y-1">
                      {r.report.summary && (
                        <div className="text-sm text-gray-800 whitespace-pre-wrap break-words">
                          {r.report.summary}
                        </div>
                      )}
                      {Object.keys(r.report.metrics).length > 0 && (
                        <div className="flex flex-wrap gap-1">
                          {Object.entries(r.report.metrics).map(([k, v]) => (
                            <span
                              key={k}
                              className="px-1.5 py-0.5 text-xs bg-gray-100 rounded font-mono"
                            >
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
                  ) : (
                    <div className="text-xs text-gray-400">指標なし</div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Section>
    </>
  );
}
