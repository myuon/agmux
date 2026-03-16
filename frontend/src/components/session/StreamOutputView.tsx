import { useState } from "react";
import type { StreamEntry } from "../../models/stream";
import { mergeStreamEntries } from "../../models/stream";
import type { ActiveTask } from "../../models/stream";
import { useAutoScroll } from "../../hooks/useAutoScroll";
import { StreamDisplayItemView } from "./StreamDisplayItemView";

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-50", label: "User", text: "text-blue-700" },
  assistant: { bg: "bg-gray-50", label: "Assistant", text: "text-green-700" },
};

type StreamViewMode = "markdown" | "json";

export function StreamOutputView({ lines, partialText, className, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded, provider }: {
  lines: unknown[];
  partialText?: string;
  className?: string;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
  provider?: string;
}) {
  const { ref, onScroll } = useAutoScroll(lines);
  const [viewMode, setViewMode] = useState<StreamViewMode>("markdown");

  const entries = lines
    .map((line) => line as StreamEntry)
    .filter((e) => e.type === "user" || e.type === "assistant" || e.type === "system");

  const groups = mergeStreamEntries(entries, partialText || undefined, provider);

  return (
    <div className={`flex flex-col ${className || ""}`}>
      <div className="flex justify-end mb-1 shrink-0">
        <button
          onClick={() => setViewMode(viewMode === "markdown" ? "json" : "markdown")}
          className="text-xs text-gray-500 hover:text-gray-700 px-2 py-1"
        >
          {viewMode === "markdown" ? "JSON" : "Markdown"}
        </button>
      </div>
      <div ref={ref} onScroll={onScroll} className="bg-white border border-gray-200 rounded-lg p-3 text-sm flex-1 min-h-0 overflow-y-auto space-y-3">
        {viewMode === "json" ? (
          lines.length === 0 ? (
            <p className="text-gray-400">No stream output yet</p>
          ) : (
            lines.map((line, i) => (
              <pre key={i} className="text-gray-600 text-xs whitespace-pre-wrap">
                {JSON.stringify(line, null, 2)}
              </pre>
            ))
          )
        ) : groups.length === 0 ? (
          <p className="text-gray-400">No stream output yet</p>
        ) : (
          groups.map((group, i) => {
            if (group.role === "system") {
              return (
                <div key={i}>
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />
                  ))}
                </div>
              );
            }
            const style = roleStyles[group.role] || roleStyles.assistant;
            return (
              <div key={i} className={`rounded-lg p-3 ${style.bg}`}>
                <div className="flex items-center gap-2 mb-1">
                  <span className={`font-semibold text-xs ${style.text}`}>
                    {style.label}
                  </span>
                </div>
                <div className="text-gray-800 break-words text-xs space-y-2">
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />
                  ))}
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

export function ActiveTasksPanel({ tasks }: { tasks: ActiveTask[] }) {
  return (
    <div className="border border-amber-200 bg-amber-50 rounded-lg p-3 space-y-2">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-amber-700">
        <span className="inline-block w-2 h-2 rounded-full bg-amber-500 animate-pulse" />
        Running Tasks ({tasks.length})
      </div>
      {tasks.map((task) => (
        <div key={task.taskId} className="bg-white rounded border border-amber-100 px-3 py-2 text-xs space-y-1">
          <div className="flex items-center gap-2">
            <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${
              task.taskType === "local_agent"
                ? "bg-purple-100 text-purple-700"
                : "bg-gray-100 text-gray-600"
            }`}>
              {task.taskType === "local_agent" ? "Agent" : task.taskType === "local_bash" ? "Bash" : task.taskType}
            </span>
            {task.lastToolName && (
              <span className="text-gray-500">
                <span className="text-gray-400">tool:</span> {task.lastToolName}
              </span>
            )}
            {task.usage && (
              <span className="text-gray-400 ml-auto">
                {task.usage.inputTokens != null && `${Math.round(task.usage.inputTokens / 1000)}k in`}
                {task.usage.outputTokens != null && ` / ${Math.round(task.usage.outputTokens / 1000)}k out`}
              </span>
            )}
          </div>
          {task.description && (
            <div className="text-gray-600 truncate">{task.description}</div>
          )}
        </div>
      ))}
    </div>
  );
}
