import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { AutomationRunReport, AutomationTask } from "../api/client";
import { Section } from "./ui/Section";
import { AlertBanner } from "./ui/AlertBanner";

export function formatDuration(sec: number): string {
  if (sec <= 0) return "-";
  const s = Math.round(sec);
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}分${s % 60}秒`;
  return `${Math.floor(m / 60)}時間${m % 60}分`;
}

export function formatNum(n: number): string {
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
export function Sparkline({ points, className = "w-full h-14" }: { points: number[]; className?: string }) {
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
    <svg viewBox={`0 0 ${w} ${h}`} className={className} role="img">
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
 * Dashboard of a loop (experimental "loops"): summary and metric trends
 * (the per-run history is LoopTimeline). The caller renders it only while on.
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
    </>
  );
}
