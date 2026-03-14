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
import type { MetricsSummary, MetricRow, MetricEvent } from "../api/client";
import { useNavigate, useLoaderData, useSearchParams } from "react-router-dom";
import { SummaryCard } from "../components/ui/SummaryCard";
import {
  ToolDurationRanking,
  ContextUsageAnalysis,
  PromptWorkflowAnalysis,
  EventsSection,
  formatCost,
  formatTokens,
  formatDuration,
} from "../components/metrics";

type TimeRange = "1h" | "6h" | "24h" | "7d" | "all";

interface MetricsLoaderData {
  summary: MetricsSummary;
  costTimeline: MetricRow[];
  tokenTimeline: MetricRow[];
  events: MetricEvent[];
  range: string;
}

export function MetricsPage() {
  const { summary, costTimeline, tokenTimeline, events, range: loaderRange } = useLoaderData<MetricsLoaderData>();
  const range = (loaderRange ?? "24h") as TimeRange;
  const [, setSearchParams] = useSearchParams();
  const navigate = useNavigate();

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
              onClick={() => setSearchParams({ range: r })}
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
        {!summary ? (
          <div className="text-center text-gray-500 py-12">No metrics data yet</div>
        ) : (
          <>
            {/* Summary cards */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
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
                label="Sessions"
                value={String(Math.round(summary.sessionCount))}
              />
              <SummaryCard
                label="Active Time"
                value={formatDuration(summary.activeTime)}
              />
              <SummaryCard
                label="Lines Changed"
                value={String(Math.round(summary.linesOfCode))}
              />
              <SummaryCard
                label="Commits"
                value={String(Math.round(summary.commitCount))}
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
                    <YAxis fontSize={10} tickFormatter={(v: number) => `$${v.toFixed(3)}`} />
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

            {/* Tool Duration Ranking */}
            <ToolDurationRanking events={events} />

            {/* Context Usage Analysis */}
            <ContextUsageAnalysis events={events} />

            {/* Prompt Workflow Analysis */}
            <PromptWorkflowAnalysis events={events} />

            {/* Events */}
            {events.length > 0 && (
              <EventsSection events={events} />
            )}
          </>
        )}
      </div>
    </div>
  );
}

