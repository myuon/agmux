import { useState } from "react";
import type { MetricEvent } from "../../api/client";
import { formatMs, formatBytes } from "./formatters";

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

export function ToolDurationRanking({ events }: { events: MetricEvent[] }) {
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
