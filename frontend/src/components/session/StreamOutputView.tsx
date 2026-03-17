import { useState } from "react";
import type { StreamEntry } from "../../models/stream";
import { mergeStreamEntries } from "../../models/stream";
import type { ActiveTask } from "../../models/stream";
import { useAutoScroll } from "../../hooks/useAutoScroll";
import { StreamDisplayItemView } from "./StreamDisplayItemView";
import { toolIcon, toolDescription } from "../../models/tool";
import { Modal } from "../ui/Modal";
import { ToolInputView } from "./ToolInputView";

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

function ActiveTaskItem({ task }: { task: ActiveTask }) {
  const [open, setOpen] = useState(false);
  const taskTypeLabel = task.taskType === "local_agent" ? "Agent" : task.taskType === "local_bash" ? "Bash" : task.taskType;
  const Icon = toolIcon(taskTypeLabel);
  const desc = task.description || (task.lastToolInput ? toolDescription(taskTypeLabel, task.lastToolInput) : null);

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <span className="relative flex shrink-0">
            <Icon className="w-3.5 h-3.5 text-gray-500 shrink-0" />
            <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
          </span>
          <span className="font-medium text-xs text-gray-800">{taskTypeLabel}</span>
          {desc && (
            <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>
          )}
          {task.usage && (
            <span className="text-[10px] text-gray-400 ml-auto shrink-0">
              {task.usage.inputTokens != null && `${Math.round(task.usage.inputTokens / 1000)}k in`}
              {task.usage.outputTokens != null && ` / ${Math.round(task.usage.outputTokens / 1000)}k out`}
            </span>
          )}
        </div>
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={
          <span className="flex items-center gap-2">
            <Icon className="w-4 h-4 text-gray-500" />
            {taskTypeLabel}
            {desc && <span className="text-gray-400 font-normal truncate">{desc}</span>}
          </span>
        }
      >
        <div className="space-y-3">
          <div>
            <span className="text-gray-400 text-[10px] uppercase tracking-wide">Task ID</span>
            <div className="text-gray-700 text-xs mt-0.5 font-mono">{task.taskId}</div>
          </div>
          {task.agentId && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Agent ID</span>
              <div className="text-gray-700 text-xs mt-0.5 font-mono">{task.agentId}</div>
            </div>
          )}
          <div>
            <span className="text-gray-400 text-[10px] uppercase tracking-wide">Task Type</span>
            <div className="text-gray-700 text-xs mt-0.5 font-mono">{taskTypeLabel}</div>
          </div>
          {task.description && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Description</span>
              <div className="text-gray-700 text-xs mt-0.5">{task.description}</div>
            </div>
          )}
          {task.lastToolInput != null && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Input</span>
              <div className="mt-0.5">
                <ToolInputView input={task.lastToolInput} />
              </div>
            </div>
          )}
          {task.output && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
              <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{task.output.slice(0, 2000)}</pre>
            </div>
          )}
          {task.usage && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Usage</span>
              <div className="text-gray-700 text-xs mt-0.5 font-mono">
                {task.usage.inputTokens != null && `Input: ${task.usage.inputTokens.toLocaleString()} tokens`}
                {task.usage.inputTokens != null && task.usage.outputTokens != null && " / "}
                {task.usage.outputTokens != null && `Output: ${task.usage.outputTokens.toLocaleString()} tokens`}
              </div>
            </div>
          )}
        </div>
      </Modal>
    </>
  );
}

export function ActiveTasksPanel({ tasks }: { tasks: ActiveTask[] }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-amber-700">
        <span className="inline-block w-2 h-2 rounded-full bg-amber-500 animate-pulse" />
        Running Tasks ({tasks.length})
      </div>
      {tasks.map((task) => (
        <ActiveTaskItem key={task.taskId} task={task} />
      ))}
    </div>
  );
}
