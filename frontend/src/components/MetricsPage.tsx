import { useCallback, useEffect, useState } from "react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  LineChart,
  Line,
  Legend,
} from "recharts";
import { api } from "../api/client";
import type { MetricsSummary, MetricRow } from "../api/client";
import { useNavigate } from "react-router-dom";

type TimeRange = "1h" | "6h" | "24h" | "7d" | "all";

function sinceFromRange(range: TimeRange): string | undefined {
  if (range === "all") return undefined;
  const ms: Record<string, number> = {
    "1h": 3600_000,
    "6h": 21600_000,
    "24h": 86400_000,
    "7d": 604800_000,
  };
  return new Date(Date.now() - ms[range]).toISOString();
}

function formatCost(usd: number): string {
  return `$${usd.toFixed(4)}`;
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(Math.round(n));
}

export function MetricsPage() {
  const [summary, setSummary] = useState<MetricsSummary | null>(null);
  const [costTimeline, setCostTimeline] = useState<MetricRow[]>([]);
  const [tokenTimeline, setTokenTimeline] = useState<MetricRow[]>([]);
  const [range, setRange] = useState<TimeRange>("24h");
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  const load = useCallback(async () => {
    setLoading(true);
    const since = sinceFromRange(range);
    try {
      const [s, cost, tokens] = await Promise.all([
        api.getMetricsSummary(since),
        api.getMetrics({ name: "claude_code.cost.usage", since }),
        api.getMetrics({ name: "claude_code.token.usage", since }),
      ]);
      setSummary(s);
      setCostTimeline(cost);
      setTokenTimeline(tokens);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [range]);

  useEffect(() => {
    load();
  }, [load]);

  // Build cost chart data: group by timestamp (minute buckets)
  const costChartData = (() => {
    const buckets = new Map<string, number>();
    for (const m of costTimeline) {
      const t = new Date(m.timestamp);
      const key = `${t.getMonth() + 1}/${t.getDate()} ${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
      buckets.set(key, (buckets.get(key) ?? 0) + m.value);
    }
    return Array.from(buckets.entries()).map(([time, cost]) => ({ time, cost }));
  })();

  // Build token chart data
  const tokenChartData = (() => {
    const buckets = new Map<string, { input: number; output: number }>();
    for (const m of tokenTimeline) {
      const t = new Date(m.timestamp);
      const key = `${t.getMonth() + 1}/${t.getDate()} ${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
      const b = buckets.get(key) ?? { input: 0, output: 0 };
      const type = m.attributes?.type ?? "input";
      if (type === "input" || type === "cacheRead" || type === "cacheCreation") {
        b.input += m.value;
      } else {
        b.output += m.value;
      }
      buckets.set(key, b);
    }
    return Array.from(buckets.entries()).map(([time, v]) => ({
      time,
      input: v.input,
      output: v.output,
    }));
  })();

  return (
    <div className="h-screen flex flex-col bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-4 md:px-8 py-3 flex items-center justify-between shrink-0">
        <div className="flex items-center gap-3">
          <button
            onClick={() => navigate("/")}
            className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M9.707 16.707a1 1 0 01-1.414 0l-6-6a1 1 0 010-1.414l6-6a1 1 0 011.414 1.414L5.414 9H17a1 1 0 110 2H5.414l4.293 4.293a1 1 0 010 1.414z" clipRule="evenodd" />
            </svg>
          </button>
          <h1 className="text-lg md:text-xl font-bold text-gray-900">Metrics</h1>
        </div>
        <div className="flex gap-1">
          {(["1h", "6h", "24h", "7d", "all"] as TimeRange[]).map((r) => (
            <button
              key={r}
              onClick={() => setRange(r)}
              className={`px-2.5 py-1 text-xs rounded ${
                range === r
                  ? "bg-blue-600 text-white"
                  : "bg-gray-100 text-gray-600 hover:bg-gray-200"
              }`}
            >
              {r}
            </button>
          ))}
        </div>
      </header>

      <div className="flex-1 overflow-y-auto p-4 md:p-6 space-y-6">
        {loading && !summary ? (
          <div className="text-center text-gray-500 py-12">Loading...</div>
        ) : !summary ? (
          <div className="text-center text-gray-500 py-12">No metrics data yet</div>
        ) : (
          <>
            {/* Summary cards */}
            <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
              <SummaryCard label="Total Cost" value={formatCost(summary.totalCost)} />
              <SummaryCard
                label="Input Tokens"
                value={formatTokens(summary.totalTokens?.input ?? 0)}
              />
              <SummaryCard
                label="Output Tokens"
                value={formatTokens(summary.totalTokens?.output ?? 0)}
              />
              <SummaryCard
                label="Cache Read"
                value={formatTokens(summary.totalTokens?.cacheRead ?? 0)}
              />
              <SummaryCard
                label="Lines Changed"
                value={String(Math.round(summary.linesOfCode))}
              />
            </div>

            {/* Cost chart */}
            {costChartData.length > 0 && (
              <div className="bg-white rounded-lg border border-gray-200 p-4">
                <h3 className="text-sm font-semibold text-gray-700 mb-3">Cost over time (USD)</h3>
                <ResponsiveContainer width="100%" height={250}>
                  <BarChart data={costChartData}>
                    <CartesianGrid strokeDasharray="3 3" />
                    <XAxis dataKey="time" fontSize={10} />
                    <YAxis fontSize={10} tickFormatter={(v) => `$${v.toFixed(3)}`} />
                    <Tooltip formatter={(v) => formatCost(Number(v))} />
                    <Bar dataKey="cost" fill="#3b82f6" />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}

            {/* Token chart */}
            {tokenChartData.length > 0 && (
              <div className="bg-white rounded-lg border border-gray-200 p-4">
                <h3 className="text-sm font-semibold text-gray-700 mb-3">Token usage over time</h3>
                <ResponsiveContainer width="100%" height={250}>
                  <LineChart data={tokenChartData}>
                    <CartesianGrid strokeDasharray="3 3" />
                    <XAxis dataKey="time" fontSize={10} />
                    <YAxis fontSize={10} tickFormatter={formatTokens} />
                    <Tooltip formatter={(v) => formatTokens(Number(v))} />
                    <Legend />
                    <Line type="monotone" dataKey="input" stroke="#3b82f6" dot={false} />
                    <Line type="monotone" dataKey="output" stroke="#ef4444" dot={false} />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            )}

            {/* Cost by session */}
            {summary.costBySession && summary.costBySession.length > 0 && (
              <div className="bg-white rounded-lg border border-gray-200 p-4">
                <h3 className="text-sm font-semibold text-gray-700 mb-3">Cost by session</h3>
                <div className="space-y-1">
                  {summary.costBySession.map((s) => (
                    <div
                      key={s.sessionId}
                      className="flex justify-between text-sm py-1 border-b border-gray-100"
                    >
                      <span className="text-gray-600 font-mono text-xs">
                        {s.sessionId.substring(0, 8)}
                      </span>
                      <span className="font-medium">{formatCost(s.cost)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function SummaryCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 p-3">
      <div className="text-xs text-gray-500">{label}</div>
      <div className="text-lg font-bold text-gray-900 mt-0.5">{value}</div>
    </div>
  );
}
