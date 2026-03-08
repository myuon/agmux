import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import type { MetricEvent } from "../../api/client";
import { formatTokens } from "./formatters";

export function ContextUsageAnalysis({ events }: { events: MetricEvent[] }) {
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
