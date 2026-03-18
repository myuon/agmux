import { useState } from "react";
import type { MetricEvent } from "../../api/client";
import { EVENT_LABELS, EVENT_COLORS } from "./constants";
import { FilterButton } from "../ui/FilterButton";

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

export function EventsSection({ events }: { events: MetricEvent[] }) {
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
          <FilterButton selected={filter === "all"} onClick={() => setFilter("all")}>
            All
          </FilterButton>
          {eventTypes.map((type) => (
            <FilterButton
              key={type}
              selected={filter === type}
              onClick={() => setFilter(type)}
            >
              {EVENT_LABELS[type] ?? type} ({counts[type]})
            </FilterButton>
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
