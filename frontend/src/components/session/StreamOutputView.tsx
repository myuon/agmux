import { useState, useRef, useCallback, useEffect } from "react";
import type { StreamEntry } from "../../models/stream";
import { mergeStreamEntries } from "../../models/stream";
import type { ActiveTask, ToolCallHistoryEntry } from "../../models/stream";
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

function ToolCallHistoryItem({ entry, index, isLatest }: { entry: ToolCallHistoryEntry; index: number; isLatest: boolean }) {
  const [expanded, setExpanded] = useState(isLatest);
  const Icon = toolIcon(entry.toolName);
  const desc = entry.input ? toolDescription(entry.toolName, entry.input) : null;

  return (
    <div className={`border rounded ${isLatest ? "border-amber-300 bg-amber-50/50" : "border-gray-200 bg-gray-50"}`}>
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-2 py-1 flex items-center gap-1.5 hover:bg-gray-100/50 transition-colors"
      >
        <span className="text-gray-400 text-[10px] font-mono w-4 text-right shrink-0">{index + 1}</span>
        <Icon className="w-3 h-3 text-gray-500 shrink-0" />
        <span className="text-xs font-medium text-gray-700">{entry.toolName}</span>
        {desc && <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>}
        {isLatest && <span className="text-[10px] text-amber-600 ml-auto shrink-0">current</span>}
        <svg className={`w-3 h-3 text-gray-400 shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4 2l4 4-4 4" /></svg>
      </button>
      {expanded && entry.input != null && (
        <div className="px-2 pb-1.5 pt-0.5 border-t border-gray-200">
          <ToolInputView input={entry.input} />
        </div>
      )}
    </div>
  );
}

function ActiveTaskItem({ task, onDismiss }: { task: ActiveTask; onDismiss?: (taskId: string) => void }) {
  const [open, setOpen] = useState(false);
  const taskTypeLabel = task.taskType === "local_agent" ? "Agent" : task.taskType === "local_bash" ? "Bash" : task.taskType;
  const Icon = toolIcon(taskTypeLabel);
  const desc = task.description || (task.lastToolInput ? toolDescription(taskTypeLabel, task.lastToolInput) : null);

  return (
    <>
      <div className="flex items-center gap-1">
        <button
          onClick={() => setOpen(true)}
          className="flex-1 min-w-0 text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
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
        {onDismiss && (
          <button
            onClick={(e) => { e.stopPropagation(); onDismiss(task.taskId); }}
            className="shrink-0 p-1 text-gray-400 hover:text-gray-600 hover:bg-gray-200 rounded transition-colors"
            title="Hide task"
          >
            <svg className="w-3 h-3" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M2 2l8 8M10 2l-8 8" />
            </svg>
          </button>
        )}
      </div>
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
          {task.toolCallHistory.length > 0 && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Tool Call History ({task.toolCallHistory.length})</span>
              <div className="mt-1 space-y-1">
                {task.toolCallHistory.map((entry, i) => (
                  <ToolCallHistoryItem key={i} entry={entry} index={i} isLatest={i === task.toolCallHistory.length - 1} />
                ))}
              </div>
            </div>
          )}
          {task.toolCallHistory.length === 0 && task.lastToolInput != null && (
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
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(new Set());
  // Track previous task snapshots to detect task_progress updates for hidden tasks
  const prevTasksRef = useRef<Map<string, ActiveTask>>(new Map());

  useEffect(() => {
    const prevMap = prevTasksRef.current;
    const updatedHidden = new Set(hiddenIds);
    let changed = false;
    for (const task of tasks) {
      if (updatedHidden.has(task.taskId)) {
        const prev = prevMap.get(task.taskId);
        // Re-show if task data changed (new task_progress event)
        if (prev && JSON.stringify(prev) !== JSON.stringify(task)) {
          updatedHidden.delete(task.taskId);
          changed = true;
        }
      }
    }
    // Update ref with current snapshot
    const newMap = new Map<string, ActiveTask>();
    for (const task of tasks) {
      newMap.set(task.taskId, { ...task });
    }
    prevTasksRef.current = newMap;
    if (changed) {
      setHiddenIds(updatedHidden);
    }
  }, [tasks, hiddenIds]);

  const handleDismiss = useCallback((taskId: string) => {
    setHiddenIds((prev) => new Set(prev).add(taskId));
  }, []);

  const visibleTasks = tasks.filter((t) => !hiddenIds.has(t.taskId));

  if (visibleTasks.length === 0) return null;

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-amber-700">
        <span className="inline-block w-2 h-2 rounded-full bg-amber-500 animate-pulse" />
        Running Tasks ({visibleTasks.length})
      </div>
      {visibleTasks.map((task) => (
        <ActiveTaskItem key={task.taskId} task={task} onDismiss={handleDismiss} />
      ))}
    </div>
  );
}
