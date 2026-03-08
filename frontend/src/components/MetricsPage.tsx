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
import type { MetricsSummary, MetricRow, MetricEvent } from "../api/client";
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

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  const h = Math.floor(seconds / 3600);
  const m = Math.round((seconds % 3600) / 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
}

function formatMs(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  if (ms < 3600000) return `${(ms / 60000).toFixed(1)}m`;
  return `${(ms / 3600000).toFixed(1)}h`;
}

export function MetricsPage() {
  const [summary, setSummary] = useState<MetricsSummary | null>(null);
  const [costTimeline, setCostTimeline] = useState<MetricRow[]>([]);
  const [tokenTimeline, setTokenTimeline] = useState<MetricRow[]>([]);
  const [events, setEvents] = useState<MetricEvent[]>([]);
  const [range, setRange] = useState<TimeRange>("24h");
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  const load = useCallback(async () => {
    setLoading(true);
    const since = sinceFromRange(range);
    try {
      const [s, cost, tokens, evts] = await Promise.all([
        api.getMetricsSummary(since),
        api.getMetrics({ name: "claude_code.cost.usage", since }),
        api.getMetrics({ name: "claude_code.token.usage", since }),
        api.getMetricsEvents({ since }),
      ]);
      setSummary(s);
      setCostTimeline(cost);
      setTokenTimeline(tokens);
      setEvents(evts);
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

const EVENT_LABELS: Record<string, string> = {
  "tool_result": "Tool Result",
  "tool_decision": "Tool Decision",
  "api_request": "API Request",
  "api_error": "API Error",
  "user_prompt": "User Prompt",
};

const EVENT_COLORS: Record<string, string> = {
  "tool_result": "bg-blue-100 text-blue-700",
  "tool_decision": "bg-purple-100 text-purple-700",
  "api_request": "bg-green-100 text-green-700",
  "api_error": "bg-red-100 text-red-700",
  "user_prompt": "bg-yellow-100 text-yellow-700",
};

function parseSubCommand(evt: MetricEvent): string | null {
  const params = evt.attributes?.tool_parameters;
  if (!params) return null;
  try {
    const parsed = JSON.parse(params);
    return parsed.bash_command ?? parsed.full_command?.split(/\s+/)[0] ?? null;
  } catch {
    return null;
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

interface ToolStatEntry {
  name: string;
  totalMs: number;
  count: number;
  avgMs: number;
  failures: number;
  totalBytes: number;
  failureErrors: { error: string; timestamp: string }[];
  subCommands?: SubCmdEntry[];
}

interface SubCmdEntry {
  name: string;
  totalMs: number;
  count: number;
  avgMs: number;
  failures: number;
  totalBytes: number;
}

function ToolDurationRanking({ events }: { events: MetricEvent[] }) {
  const [expandedTools, setExpandedTools] = useState<Set<string>>(new Set());
  const toolResults = events.filter((e) => e.name === "tool_result");
  if (toolResults.length === 0) return null;

  // Aggregate by tool_name
  const toolStats = new Map<string, {
    totalMs: number; count: number; failures: number; totalBytes: number;
    failureErrors: { error: string; timestamp: string }[];
    subCmds: Map<string, { totalMs: number; count: number; failures: number; totalBytes: number }>;
  }>();
  for (const evt of toolResults) {
    const name = evt.attributes?.tool_name ?? "unknown";
    const durationMs = parseFloat(evt.attributes?.duration_ms ?? "0");
    const bytes = parseInt(evt.attributes?.tool_result_size_bytes ?? "0", 10);
    const failed = evt.attributes?.success === "false";
    if (!toolStats.has(name)) {
      toolStats.set(name, { totalMs: 0, count: 0, failures: 0, totalBytes: 0, failureErrors: [], subCmds: new Map() });
    }
    const stats = toolStats.get(name)!;
    stats.totalMs += durationMs;
    stats.count += 1;
    stats.totalBytes += bytes;
    if (failed) {
      stats.failures += 1;
      stats.failureErrors.push({
        error: evt.attributes?.error ?? "unknown error",
        timestamp: evt.timestamp,
      });
    }

    const subCmd = parseSubCommand(evt);
    if (subCmd) {
      const sub = stats.subCmds.get(subCmd) ?? { totalMs: 0, count: 0, failures: 0, totalBytes: 0 };
      sub.totalMs += durationMs;
      sub.count += 1;
      sub.totalBytes += bytes;
      if (failed) sub.failures += 1;
      stats.subCmds.set(subCmd, sub);
    }
  }

  const ranked: ToolStatEntry[] = [...toolStats.entries()]
    .map(([name, stats]) => {
      const entry: ToolStatEntry = {
        name, totalMs: stats.totalMs, count: stats.count, avgMs: stats.totalMs / stats.count,
        failures: stats.failures, totalBytes: stats.totalBytes, failureErrors: stats.failureErrors,
      };
      if (stats.subCmds.size > 1) {
        entry.subCommands = [...stats.subCmds.entries()]
          .map(([cmd, s]) => ({ name: cmd, totalMs: s.totalMs, count: s.count, avgMs: s.totalMs / s.count, failures: s.failures, totalBytes: s.totalBytes }))
          .sort((a, b) => b.totalMs - a.totalMs);
      }
      return entry;
    })
    .sort((a, b) => b.totalMs - a.totalMs);

  const maxMs = ranked[0]?.totalMs ?? 1;

  const toggleExpand = (name: string) => {
    setExpandedTools((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };

  // Total result bytes
  const totalBytes = ranked.reduce((s, t) => s + t.totalBytes, 0);
  const totalFailures = ranked.reduce((s, t) => s + t.failures, 0);

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-4">
      <h3 className="text-sm font-semibold text-gray-700 mb-1">
        Tool Duration Ranking ({toolResults.length} calls)
      </h3>
      <div className="text-xs text-gray-400 mb-3">
        Total result size: {formatBytes(totalBytes)}
        {totalFailures > 0 && <span className="text-red-400 ml-2">{totalFailures} failures</span>}
      </div>
      <div className="space-y-2">
        {ranked.map((tool) => {
          const hasDetails = (tool.subCommands && tool.subCommands.length > 0) || tool.failures > 0;
          const isExpanded = expandedTools.has(tool.name);
          return (
            <div key={tool.name}>
              <div
                className={`flex items-center gap-2 text-xs ${hasDetails ? "cursor-pointer hover:bg-gray-50 rounded -mx-1 px-1" : ""}`}
                onClick={() => hasDetails && toggleExpand(tool.name)}
              >
                <span className="w-28 text-gray-700 font-mono truncate shrink-0 flex items-center gap-1" title={tool.name}>
                  {hasDetails && <span className="text-gray-400 text-[10px]">{isExpanded ? "▼" : "▶"}</span>}
                  {tool.name}
                </span>
                <div className="flex-1 bg-gray-100 rounded-full h-4 relative overflow-hidden">
                  <div
                    className="bg-blue-500 h-full rounded-full"
                    style={{ width: `${(tool.totalMs / maxMs) * 100}%` }}
                  />
                </div>
                <div className="text-right text-gray-500 shrink-0 flex gap-2 items-center">
                  <span className="font-medium text-gray-700">{formatMs(tool.totalMs)}</span>
                  <span className="w-28">{tool.count}x, avg {formatMs(tool.avgMs)}</span>
                  <span className="w-16 text-right">{formatBytes(tool.totalBytes)}</span>
                  {tool.failures > 0 && (
                    <span className="text-red-500 w-10 text-right">{tool.failures}err</span>
                  )}
                  {tool.failures === 0 && <span className="w-10" />}
                </div>
              </div>
              {hasDetails && isExpanded && (
                <div className="ml-6 mt-1 space-y-1 border-l-2 border-gray-200 pl-3">
                  {/* Sub-commands */}
                  {tool.subCommands?.map((sub) => (
                    <div key={sub.name} className="flex items-center gap-2 text-xs">
                      <span className="w-24 text-gray-500 font-mono truncate shrink-0" title={sub.name}>
                        {sub.name}
                      </span>
                      <div className="flex-1 bg-gray-100 rounded-full h-3 relative overflow-hidden">
                        <div
                          className="bg-blue-300 h-full rounded-full"
                          style={{ width: `${(sub.totalMs / tool.totalMs) * 100}%` }}
                        />
                      </div>
                      <div className="text-right text-gray-400 shrink-0 flex gap-2 items-center">
                        <span className="font-medium text-gray-600">{formatMs(sub.totalMs)}</span>
                        <span className="w-28">{sub.count}x, avg {formatMs(sub.avgMs)}</span>
                        <span className="w-16 text-right">{formatBytes(sub.totalBytes)}</span>
                        {sub.failures > 0 && (
                          <span className="text-red-400 w-10 text-right">{sub.failures}err</span>
                        )}
                        {sub.failures === 0 && <span className="w-10" />}
                      </div>
                    </div>
                  ))}
                  {/* Failure details */}
                  {tool.failureErrors.length > 0 && (
                    <div className="mt-2">
                      <div className="text-[10px] text-red-500 font-medium mb-1">Failures:</div>
                      {tool.failureErrors.map((f, i) => (
                        <div key={i} className="text-[10px] text-red-400 bg-red-50 rounded px-2 py-1 mb-0.5 font-mono break-all">
                          <span className="text-gray-400 mr-2">{new Date(f.timestamp).toLocaleTimeString()}</span>
                          {f.error}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ContextUsageAnalysis({ events }: { events: MetricEvent[] }) {
  const apiRequests = events.filter((e) => e.name === "api_request");
  const toolResults = events.filter((e) => e.name === "tool_result");
  if (apiRequests.length === 0) return null;

  // Per-prompt context analysis
  const promptCalls = new Map<string, { totalInput: number; output: number; ts: string }[]>();
  for (const evt of apiRequests) {
    const pid = evt.attributes?.["prompt.id"];
    if (!pid) continue;
    const a = evt.attributes;
    const totalInput = parseInt(a?.input_tokens ?? "0", 10)
      + parseInt(a?.cache_read_tokens ?? "0", 10)
      + parseInt(a?.cache_creation_tokens ?? "0", 10);
    if (!promptCalls.has(pid)) promptCalls.set(pid, []);
    promptCalls.get(pid)!.push({ totalInput, output: parseInt(a?.output_tokens ?? "0", 10), ts: evt.timestamp });
  }

  // Compute stats per prompt
  const promptStats = [...promptCalls.entries()].map(([pid, calls]) => {
    calls.sort((a, b) => a.ts.localeCompare(b.ts));
    const maxCtx = Math.max(...calls.map((c) => c.totalInput));
    const firstCtx = calls[0].totalInput;
    return { pid, calls: calls.length, firstCtx, maxCtx, growth: maxCtx - firstCtx };
  });

  // Overall stats
  const allMax = promptStats.map((p) => p.maxCtx);
  const avgMax = allMax.reduce((s, v) => s + v, 0) / allMax.length;
  const sortedMax = [...allMax].sort((a, b) => a - b);
  const medianMax = sortedMax[Math.floor(sortedMax.length / 2)];
  const largestCtx = Math.max(...allMax);

  // Distribution of max context sizes
  const ctxBuckets = new Map<string, number>();
  const bucketOrder = ["<10K", "10-30K", "30-60K", "60-100K", "100-150K", "150K+"];
  for (const m of allMax) {
    let bucket: string;
    if (m < 10000) bucket = "<10K";
    else if (m < 30000) bucket = "10-30K";
    else if (m < 60000) bucket = "30-60K";
    else if (m < 100000) bucket = "60-100K";
    else if (m < 150000) bucket = "100-150K";
    else bucket = "150K+";
    ctxBuckets.set(bucket, (ctxBuckets.get(bucket) ?? 0) + 1);
  }
  const ctxDistData = bucketOrder
    .filter((k) => ctxBuckets.has(k))
    .map((bucket) => ({ bucket, count: ctxBuckets.get(bucket)! }));

  // Tool contribution to context (bytes → estimated tokens)
  const toolCtx = new Map<string, { totalBytes: number; count: number }>();
  for (const evt of toolResults) {
    const name = evt.attributes?.tool_name ?? "unknown";
    const bytes = parseInt(evt.attributes?.tool_result_size_bytes ?? "0", 10);
    const stats = toolCtx.get(name) ?? { totalBytes: 0, count: 0 };
    stats.totalBytes += bytes;
    stats.count += 1;
    toolCtx.set(name, stats);
  }
  const toolCtxRanked = [...toolCtx.entries()]
    .map(([name, s]) => ({ name, estTokens: Math.round(s.totalBytes / 4), count: s.count, avgTokens: Math.round(s.totalBytes / 4 / s.count) }))
    .sort((a, b) => b.estTokens - a.estTokens);
  const maxToolTokens = toolCtxRanked[0]?.estTokens ?? 1;

  // Top prompts by max context
  const topByCtx = [...promptStats].sort((a, b) => b.maxCtx - a.maxCtx).slice(0, 10);

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-4 space-y-4">
      <h3 className="text-sm font-semibold text-gray-700">Context Usage Analysis</h3>

      {/* Summary stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Avg Max Context</div>
          <div className="text-lg font-bold text-gray-900">{formatTokens(avgMax)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Median Max Context</div>
          <div className="text-lg font-bold text-gray-900">{formatTokens(medianMax)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Largest Context</div>
          <div className="text-lg font-bold text-gray-900">{formatTokens(largestCtx)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Prompts Analyzed</div>
          <div className="text-lg font-bold text-gray-900">{promptStats.length}</div>
        </div>
      </div>

      {/* Context size distribution */}
      {ctxDistData.length > 0 && (
        <div>
          <div className="text-xs text-gray-500 mb-2">Context size distribution (max tokens per prompt)</div>
          <ResponsiveContainer width="100%" height={160}>
            <BarChart data={ctxDistData}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" fontSize={10} />
              <YAxis fontSize={10} />
              <Tooltip formatter={(v) => `${v} prompts`} />
              <Bar dataKey="count" fill="#8b5cf6" name="Prompts" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* Tool contribution to context */}
      <div>
        <div className="text-xs text-gray-500 mb-2">Context contribution by tool (estimated tokens from result size)</div>
        <div className="space-y-1.5">
          {toolCtxRanked.map((tool) => (
            <div key={tool.name} className="flex items-center gap-2 text-xs">
              <span className="w-28 text-gray-700 font-mono truncate shrink-0">{tool.name}</span>
              <div className="flex-1 bg-gray-100 rounded-full h-3 relative overflow-hidden">
                <div
                  className="bg-purple-400 h-full rounded-full"
                  style={{ width: `${(tool.estTokens / maxToolTokens) * 100}%` }}
                />
              </div>
              <div className="text-right text-gray-500 shrink-0 flex gap-2 items-center">
                <span className="font-medium text-gray-700">~{formatTokens(tool.estTokens)}</span>
                <span className="w-32">{tool.count}x, avg ~{formatTokens(tool.avgTokens)}/call</span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Top prompts by context size */}
      <div>
        <div className="text-xs text-gray-500 mb-2">Top prompts by max context size</div>
        <div className="space-y-1">
          {topByCtx.map((p) => {
            const pct = largestCtx > 0 ? (p.maxCtx / 200000) * 100 : 0; // 200K as reference max
            return (
              <div key={p.pid} className="flex items-center gap-2 text-xs">
                <span className="w-16 text-gray-400 font-mono shrink-0">{p.pid.substring(0, 8)}</span>
                <div className="flex-1 bg-gray-100 rounded-full h-3 relative overflow-hidden">
                  <div
                    className={`h-full rounded-full ${pct > 75 ? "bg-red-400" : pct > 50 ? "bg-amber-400" : "bg-purple-400"}`}
                    style={{ width: `${Math.min(pct, 100)}%` }}
                  />
                </div>
                <span className="w-44 text-gray-500 shrink-0 text-right">
                  {formatTokens(p.maxCtx)} ({p.calls} calls{p.growth > 0 ? `, +${formatTokens(p.growth)}` : ""})
                </span>
              </div>
            );
          })}
        </div>
        <div className="flex gap-4 mt-2 text-[10px] text-gray-400">
          <span className="flex items-center gap-1"><span className="w-3 h-2 bg-purple-400 rounded inline-block" /> &lt;50%</span>
          <span className="flex items-center gap-1"><span className="w-3 h-2 bg-amber-400 rounded inline-block" /> 50-75%</span>
          <span className="flex items-center gap-1"><span className="w-3 h-2 bg-red-400 rounded inline-block" /> &gt;75%</span>
          <span className="text-gray-300">of 200K limit</span>
        </div>
      </div>
    </div>
  );
}

interface PromptStats {
  promptId: string;
  apiCalls: number;
  toolCalls: number;
  apiMs: number;
  toolMs: number;
  costUsd: number;
  timestamp: string;
}

function buildPromptStats(events: MetricEvent[]): PromptStats[] {
  const map = new Map<string, PromptStats>();
  for (const evt of events) {
    const pid = evt.attributes?.["prompt.id"];
    if (!pid) continue;
    if (!map.has(pid)) {
      map.set(pid, { promptId: pid, apiCalls: 0, toolCalls: 0, apiMs: 0, toolMs: 0, costUsd: 0, timestamp: evt.timestamp });
    }
    const s = map.get(pid)!;
    const ms = parseFloat(evt.attributes?.duration_ms ?? "0");
    if (evt.name === "api_request") {
      s.apiCalls += 1;
      s.apiMs += ms;
      s.costUsd += parseFloat(evt.attributes?.cost_usd ?? "0");
    } else if (evt.name === "tool_result") {
      s.toolCalls += 1;
      s.toolMs += ms;
    }
  }
  return [...map.values()];
}

function PromptWorkflowAnalysis({ events }: { events: MetricEvent[] }) {
  const prompts = buildPromptStats(events);
  if (prompts.length === 0) return null;

  // Averages
  const n = prompts.length;
  const avgApiCalls = prompts.reduce((s, p) => s + p.apiCalls, 0) / n;
  const avgToolCalls = prompts.reduce((s, p) => s + p.toolCalls, 0) / n;
  const avgApiMs = prompts.reduce((s, p) => s + p.apiMs, 0) / n;
  const avgToolMs = prompts.reduce((s, p) => s + p.toolMs, 0) / n;
  const avgTotal = avgApiMs + avgToolMs;
  const modelRatio = avgTotal > 0 ? (avgApiMs / avgTotal) * 100 : 0;
  const avgCost = prompts.reduce((s, p) => s + p.costUsd, 0) / n;

  // Distribution buckets for the chart: group prompts by API call count
  const distMap = new Map<string, { count: number; avgMs: number }>();
  for (const p of prompts) {
    let bucket: string;
    if (p.apiCalls <= 1) bucket = "1";
    else if (p.apiCalls <= 3) bucket = "2-3";
    else if (p.apiCalls <= 5) bucket = "4-5";
    else if (p.apiCalls <= 10) bucket = "6-10";
    else if (p.apiCalls <= 20) bucket = "11-20";
    else bucket = "21+";
    const d = distMap.get(bucket) ?? { count: 0, avgMs: 0 };
    d.count += 1;
    d.avgMs += p.apiMs + p.toolMs;
    distMap.set(bucket, d);
  }
  const distOrder = ["1", "2-3", "4-5", "6-10", "11-20", "21+"];
  const distData = distOrder
    .filter((k) => distMap.has(k))
    .map((bucket) => {
      const d = distMap.get(bucket)!;
      return { bucket: `${bucket} calls`, count: d.count, avgTime: d.avgMs / d.count / 1000 };
    });

  // Top prompts by total time
  const topPrompts = [...prompts]
    .sort((a, b) => (b.apiMs + b.toolMs) - (a.apiMs + a.toolMs))
    .slice(0, 10);

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-4 space-y-4">
      <h3 className="text-sm font-semibold text-gray-700">
        Prompt Workflow Analysis ({n} prompts)
      </h3>

      {/* Average stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Avg API Calls</div>
          <div className="text-lg font-bold text-gray-900">{avgApiCalls.toFixed(1)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Avg Tool Calls</div>
          <div className="text-lg font-bold text-gray-900">{avgToolCalls.toFixed(1)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Avg Total Time</div>
          <div className="text-lg font-bold text-gray-900">{formatMs(avgTotal)}</div>
        </div>
        <div className="bg-gray-50 rounded p-3">
          <div className="text-xs text-gray-500">Avg Cost / Prompt</div>
          <div className="text-lg font-bold text-gray-900">{formatCost(avgCost)}</div>
        </div>
      </div>

      {/* Time split bar */}
      <div>
        <div className="text-xs text-gray-500 mb-1">
          Average time split: Model {modelRatio.toFixed(0)}% / Tool {(100 - modelRatio).toFixed(0)}%
        </div>
        <div className="flex h-5 rounded-full overflow-hidden">
          <div
            className="bg-blue-500 flex items-center justify-center text-white text-[10px] font-medium"
            style={{ width: `${modelRatio}%` }}
          >
            {modelRatio > 15 ? `Model ${formatMs(avgApiMs)}` : ""}
          </div>
          <div
            className="bg-amber-500 flex items-center justify-center text-white text-[10px] font-medium"
            style={{ width: `${100 - modelRatio}%` }}
          >
            {(100 - modelRatio) > 15 ? `Tool ${formatMs(avgToolMs)}` : ""}
          </div>
        </div>
      </div>

      {/* Distribution chart */}
      {distData.length > 0 && (
        <div>
          <div className="text-xs text-gray-500 mb-2">Prompt distribution by API call count</div>
          <ResponsiveContainer width="100%" height={180}>
            <BarChart data={distData}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" fontSize={10} />
              <YAxis fontSize={10} />
              <Tooltip
                formatter={(v, name) =>
                  name === "Prompts" ? `${v} prompts` : `${Number(v).toFixed(1)}s`
                }
              />
              <Legend />
              <Bar dataKey="count" fill="#3b82f6" name="Prompts" />
              <Bar dataKey="avgTime" fill="#f59e0b" name="Avg Time (s)" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* Top prompts by time */}
      <div>
        <div className="text-xs text-gray-500 mb-2">Top prompts by total time</div>
        <div className="space-y-1">
          {topPrompts.map((p) => {
            const total = p.apiMs + p.toolMs;
            const modelPct = total > 0 ? (p.apiMs / total) * 100 : 0;
            return (
              <div key={p.promptId} className="flex items-center gap-2 text-xs">
                <span className="w-16 text-gray-400 font-mono shrink-0">
                  {p.promptId.substring(0, 8)}
                </span>
                <div className="flex-1 flex h-3 rounded-full overflow-hidden bg-gray-100">
                  <div className="bg-blue-500" style={{ width: `${modelPct}%` }} />
                  <div className="bg-amber-500" style={{ width: `${100 - modelPct}%` }} />
                </div>
                <span className="w-48 text-gray-500 shrink-0 text-right">
                  {formatMs(total)} ({p.apiCalls} API, {p.toolCalls} tools, {formatCost(p.costUsd)})
                </span>
              </div>
            );
          })}
        </div>
        <div className="flex gap-4 mt-2 text-[10px] text-gray-400">
          <span className="flex items-center gap-1"><span className="w-3 h-2 bg-blue-500 rounded inline-block" /> Model</span>
          <span className="flex items-center gap-1"><span className="w-3 h-2 bg-amber-500 rounded inline-block" /> Tool</span>
        </div>
      </div>
    </div>
  );
}

function EventsSection({ events }: { events: MetricEvent[] }) {
  const [filter, setFilter] = useState<string>("all");
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const eventTypes = [...new Set(events.map((e) => e.name))].sort();
  const filtered = filter === "all" ? events : events.filter((e) => e.name === filter);
  // Show latest first, limit to 200
  const displayed = [...filtered].reverse().slice(0, 200);

  const toggleExpand = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // Aggregate counts by event type
  const counts = events.reduce<Record<string, number>>((acc, e) => {
    acc[e.name] = (acc[e.name] ?? 0) + 1;
    return acc;
  }, {});

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-gray-700">Events ({events.length})</h3>
        <div className="flex gap-1 flex-wrap">
          <button
            onClick={() => setFilter("all")}
            className={`px-2 py-0.5 text-xs rounded ${
              filter === "all" ? "bg-gray-800 text-white" : "bg-gray-100 text-gray-600 hover:bg-gray-200"
            }`}
          >
            All
          </button>
          {eventTypes.map((type) => (
            <button
              key={type}
              onClick={() => setFilter(type)}
              className={`px-2 py-0.5 text-xs rounded ${
                filter === type ? "bg-gray-800 text-white" : "bg-gray-100 text-gray-600 hover:bg-gray-200"
              }`}
            >
              {EVENT_LABELS[type] ?? type} ({counts[type]})
            </button>
          ))}
        </div>
      </div>
      <div className="space-y-1 max-h-96 overflow-y-auto">
        {displayed.map((evt) => {
          const isExpanded = expanded.has(evt.id);
          return (
            <div
              key={evt.id}
              className="border border-gray-100 rounded p-2 cursor-pointer hover:bg-gray-50"
              onClick={() => toggleExpand(evt.id)}
            >
              <div className="flex items-center gap-2 text-xs">
                <span className={`px-1.5 py-0.5 rounded font-medium ${EVENT_COLORS[evt.name] ?? "bg-gray-100 text-gray-700"}`}>
                  {EVENT_LABELS[evt.name] ?? evt.name}
                </span>
                <span className="text-gray-400 font-mono">
                  {new Date(evt.timestamp).toLocaleTimeString()}
                </span>
                {evt.sessionId && (
                  <span className="text-gray-400 font-mono">{evt.sessionId.substring(0, 8)}</span>
                )}
                <EventSummaryText event={evt} />
              </div>
              {isExpanded && (
                <div className="mt-2 text-xs space-y-1">
                  {evt.body && (
                    <div className="bg-gray-50 rounded p-2 font-mono text-gray-600 whitespace-pre-wrap break-all">
                      {evt.body}
                    </div>
                  )}
                  {evt.attributes && Object.keys(evt.attributes).length > 0 && (
                    <div className="bg-gray-50 rounded p-2">
                      {Object.entries(evt.attributes).map(([k, v]) => (
                        <div key={k} className="flex gap-2">
                          <span className="text-gray-500 font-mono">{k}:</span>
                          <span className="text-gray-700 font-mono break-all">{v}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function EventSummaryText({ event }: { event: MetricEvent }) {
  const attrs = event.attributes ?? {};
  switch (event.name) {
    case "tool_result":
      return (
        <span className="text-gray-500 truncate">
          {attrs.tool_name}{attrs.success === "false" ? " (failed)" : ""}
          {attrs.duration_ms ? ` ${attrs.duration_ms}ms` : ""}
        </span>
      );
    case "api_request":
      return (
        <span className="text-gray-500 truncate">
          {attrs.model} {attrs.duration_ms ? `${attrs.duration_ms}ms` : ""}
          {attrs.cost_usd ? ` $${parseFloat(attrs.cost_usd).toFixed(4)}` : ""}
        </span>
      );
    case "api_error":
      return <span className="text-red-500 truncate">{attrs.error_message ?? attrs.status_code}</span>;
    case "user_prompt":
      return <span className="text-gray-500 truncate">{attrs.prompt_length ? `${attrs.prompt_length} chars` : ""}</span>;
    case "tool_decision":
      return <span className="text-gray-500 truncate">{attrs.decision} {attrs.tool_name}</span>;
    default:
      return null;
  }
}

function SummaryCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 p-3">
      <div className="text-xs text-gray-500">{label}</div>
      <div className="text-lg font-bold text-gray-900 mt-0.5">{value}</div>
    </div>
  );
}
