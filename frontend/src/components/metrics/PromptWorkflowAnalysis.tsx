import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import type { MetricEvent } from "../../api/client";
import { formatMs, formatCost } from "./formatters";

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

export function PromptWorkflowAnalysis({ events }: { events: MetricEvent[] }) {
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
