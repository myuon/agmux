import { useState, useMemo, useRef, useCallback, useEffect } from "react";
import type { StreamEntry } from "../../models/stream";
import { mergeStreamEntries } from "../../models/stream";
import type { ActiveTask, ToolCallHistoryEntry } from "../../models/stream";
import { useAutoScroll } from "../../hooks/useAutoScroll";
import { StreamDisplayItemView } from "./StreamDisplayItemView";
import { toolIcon, toolDescription } from "../../models/tool";
import { Modal } from "../ui/Modal";
import { ToolInputView } from "./ToolInputView";
import { RefreshCw } from "lucide-react";
import { motion } from "motion/react";
import { api } from "../../api/client";

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-50", label: "User", text: "text-blue-700" },
  assistant: { bg: "bg-gray-50", label: "Assistant", text: "text-green-700" },
};

type StreamViewMode = "markdown" | "json";

// Known top-level entry types that the markdown view supports
const KNOWN_ENTRY_TYPES = new Set(["user", "assistant", "system", "rate_limit_event", "result"]);

// Build a one-line summary for a stream entry (used in JSON view)
function summarizeEntry(line: unknown): { type: string; subtype?: string; summary: string; isUnknown: boolean } {
  if (line == null || typeof line !== "object") {
    return { type: typeof line, summary: String(line), isUnknown: true };
  }
  const raw = line as Record<string, unknown>;
  const type = typeof raw.type === "string" ? raw.type : "(no type)";
  const isUnknown = !KNOWN_ENTRY_TYPES.has(type);

  if (type === "assistant" || type === "user") {
    const msg = raw.message as Record<string, unknown> | undefined;
    const content = msg?.content ?? raw.content;
    let summary = "";
    const parts: string[] = [];
    if (typeof content === "string") {
      summary = content;
    } else if (Array.isArray(content)) {
      for (const block of content as Array<Record<string, unknown>>) {
        const bt = block.type;
        if (bt === "text" && typeof block.text === "string") {
          parts.push(block.text);
        } else if (bt === "tool_use" && typeof block.name === "string") {
          parts.push(`[tool_use: ${block.name}]`);
        } else if (bt === "tool_result") {
          const c = block.content;
          if (typeof c === "string") parts.push(`[tool_result: ${c.slice(0, 40)}]`);
          else parts.push("[tool_result]");
        } else if (bt === "thinking" && typeof block.thinking === "string") {
          parts.push(`[thinking: ${block.thinking}]`);
        } else if (bt === "image") {
          parts.push("[image]");
        } else if (typeof bt === "string") {
          parts.push(`[${bt}]`);
        }
      }
      summary = parts.join(" ");
    }
    summary = summary.replace(/\s+/g, " ").trim();
    return { type, summary, isUnknown };
  }

  if (type === "system") {
    const subtype = typeof raw.subtype === "string" ? raw.subtype : undefined;
    const extras: string[] = [];
    if (typeof raw.status === "string") extras.push(`status=${raw.status}`);
    if (typeof raw.task_id === "string") extras.push(`task_id=${(raw.task_id as string).slice(0, 8)}`);
    if (typeof raw.task_type === "string") extras.push(`task_type=${raw.task_type}`);
    return { type, subtype, summary: extras.join(" "), isUnknown };
  }

  if (type === "result") {
    const subtype = typeof raw.subtype === "string" ? raw.subtype : undefined;
    const isError = raw.is_error === true;
    const result = typeof raw.result === "string" ? (raw.result as string).slice(0, 60) : "";
    return { type, subtype, summary: `${isError ? "error " : ""}${result}`.trim(), isUnknown };
  }

  if (type === "rate_limit_event") {
    const info = raw.rate_limit_info as Record<string, unknown> | undefined;
    const parts: string[] = [];
    if (info) {
      if (typeof info.rateLimitType === "string") parts.push(info.rateLimitType);
      if (typeof info.status === "string") parts.push(`status=${info.status}`);
    }
    return { type, summary: parts.join(" "), isUnknown };
  }

  // Unknown / unsupported type: surface a few primitive fields as a hint
  const hintParts: string[] = [];
  for (const [k, v] of Object.entries(raw)) {
    if (k === "type") continue;
    if (hintParts.length >= 3) break;
    if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") {
      const s = String(v);
      hintParts.push(`${k}=${s.length > 30 ? s.slice(0, 30) + "..." : s}`);
    }
  }
  return { type, summary: hintParts.join(" "), isUnknown };
}

export function StreamOutputView({ lines, partialText, className, onAnswer, sessionId, pendingPermission, onPermissionResponded, provider }: {
  lines: unknown[];
  partialText?: string;
  className?: string;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  pendingPermission?: { id: string; toolName: string; input: unknown; timedOut?: boolean; timeoutSeconds?: number };
  onPermissionResponded?: () => void;
  provider?: string;
}) {
  const { ref, onScroll } = useAutoScroll(lines);
  const [viewMode, setViewMode] = useState<StreamViewMode>("markdown");
  const [selectedJsonEntry, setSelectedJsonEntry] = useState<{ index: number; line: unknown } | null>(null);
  // Persist scroll ratio across view-mode switches so position is roughly preserved
  const scrollRatioRef = useRef<number>(0);
  const prevViewModeRef = useRef<StreamViewMode>("markdown");

  // Track the current scroll ratio (scrollTop / scrollHeight)
  const handleScroll = useCallback(() => {
    if (ref.current) {
      const el = ref.current;
      const denom = el.scrollHeight - el.clientHeight;
      scrollRatioRef.current = denom > 0 ? el.scrollTop / denom : 0;
    }
    onScroll();
  }, [ref, onScroll]);

  // Restore scroll ratio after view-mode switches
  useEffect(() => {
    if (prevViewModeRef.current !== viewMode && ref.current) {
      const el = ref.current;
      const denom = el.scrollHeight - el.clientHeight;
      if (denom > 0) {
        el.scrollTop = scrollRatioRef.current * denom;
      }
    }
    prevViewModeRef.current = viewMode;
  }, [viewMode, ref]);

  const entries = lines
    .map((line) => line as StreamEntry)
    .filter((e) => e.type === "user" || e.type === "assistant" || e.type === "system" || e.type === "rate_limit_event" || e.type === "result");

  const groups = mergeStreamEntries(entries, partialText || undefined, provider);

  // Show api_retry only when the last event is a retry (transient indicator)
  const trailingRetry = useMemo(() => {
    for (let i = entries.length - 1; i >= 0; i--) {
      const e = entries[i];
      if (e.type === "system" && (e as unknown as Record<string, unknown>).subtype === "api_retry") {
        const raw = e as unknown as Record<string, unknown>;
        return {
          attempt: (raw.attempt as number) || 0,
          maxRetries: (raw.max_retries as number) || 0,
          retryDelayMs: (raw.retry_delay_ms as number) || 0,
          errorStatus: raw.error_status as number | undefined,
          error: raw.error as string | undefined,
        };
      }
      // Stop at the first non-retry event
      break;
    }
    return null;
  }, [entries]);

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
      <div ref={ref} onScroll={handleScroll} className={`bg-white border border-gray-200 rounded-lg p-3 text-sm flex-1 min-h-0 overflow-y-auto ${viewMode === "json" ? "" : "space-y-3"}`}>
        {viewMode === "json" ? (
          lines.length === 0 ? (
            <p className="text-gray-400">No stream output yet</p>
          ) : (
            <ul className="divide-y divide-gray-100 font-mono">
              {lines.map((line, i) => {
                const { type, subtype, summary, isUnknown } = summarizeEntry(line);
                return (
                  <li key={i}>
                    <button
                      type="button"
                      onClick={() => setSelectedJsonEntry({ index: i, line })}
                      className="w-full text-left flex items-center gap-2 px-1 py-1 hover:bg-gray-50 cursor-pointer text-[11px]"
                      title="Click for full JSON"
                    >
                      <span className="text-gray-400 w-8 text-right shrink-0">{i}</span>
                      <span
                        className={`shrink-0 px-1.5 rounded font-semibold ${
                          isUnknown
                            ? "bg-amber-100 text-amber-800 ring-1 ring-amber-300"
                            : "bg-gray-100 text-gray-700"
                        }`}
                      >
                        {type}
                        {subtype ? `:${subtype}` : ""}
                      </span>
                      <span className="text-gray-600 truncate min-w-0 flex-1">{summary}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )
        ) : groups.length === 0 ? (
          <p className="text-gray-400">No stream output yet</p>
        ) : (
          groups.map((group, i) => {
            if (group.role === "system") {
              return (
                <motion.div
                  key={i}
                  initial={{ opacity: 0, y: 12 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ type: "spring", damping: 25, stiffness: 300 }}
                >
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} pendingPermission={pendingPermission} onPermissionResponded={onPermissionResponded} />
                  ))}
                </motion.div>
              );
            }
            const style = roleStyles[group.role] || roleStyles.assistant;
            return (
              <motion.div
                key={i}
                className={`rounded-lg p-3 ${style.bg}`}
                initial={{ opacity: 0, y: 12 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ type: "spring", damping: 25, stiffness: 300 }}
              >
                <div className="flex items-center gap-2 mb-1">
                  <span className={`font-semibold text-xs ${style.text}`}>
                    {style.label}
                  </span>
                </div>
                <div className="text-gray-800 break-words text-xs space-y-2">
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} pendingPermission={pendingPermission} onPermissionResponded={onPermissionResponded} />
                  ))}
                </div>
              </motion.div>
            );
          })
        )}
        {trailingRetry && (
          <div className="flex items-center gap-2 py-1.5 px-3 text-xs text-amber-600 bg-amber-50 border-y border-dashed border-amber-200">
            <RefreshCw className="w-3.5 h-3.5 shrink-0 animate-spin" />
            <span className="font-medium shrink-0">APIリトライ中 ({trailingRetry.attempt}/{trailingRetry.maxRetries})</span>
            {trailingRetry.error && (
              <span className="text-amber-500 shrink-0">
                {trailingRetry.error}{trailingRetry.errorStatus ? ` (${trailingRetry.errorStatus})` : ""}
              </span>
            )}
            <span className="shrink-0 text-amber-400 ml-auto">
              待機 {trailingRetry.retryDelayMs >= 60000
                ? `${(trailingRetry.retryDelayMs / 60000).toFixed(1)}分`
                : `${(trailingRetry.retryDelayMs / 1000).toFixed(1)}秒`}
            </span>
          </div>
        )}
      </div>
      <Modal
        open={selectedJsonEntry !== null}
        onClose={() => setSelectedJsonEntry(null)}
        title={
          <span className="font-mono">
            #{selectedJsonEntry?.index ?? ""}{" "}
            {selectedJsonEntry ? summarizeEntry(selectedJsonEntry.line).type : ""}
          </span>
        }
      >
        {selectedJsonEntry && (
          <pre className="text-gray-700 text-xs whitespace-pre-wrap break-all font-mono">
            {JSON.stringify(selectedJsonEntry.line, null, 2)}
          </pre>
        )}
      </Modal>
    </div>
  );
}

function ToolCallHistoryItem({ entry, index, isLatest }: { entry: ToolCallHistoryEntry; index: number; isLatest: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const Icon = toolIcon(entry.toolName);
  const desc = entry.description || (entry.input ? toolDescription(entry.toolName, entry.input) : null);
  const hasDetail = desc || entry.input != null;

  return (
    <div className={`border rounded ${isLatest ? "border-amber-300 bg-amber-50/50" : "border-gray-200 bg-gray-50"}`}>
      <button
        onClick={() => hasDetail && setExpanded(!expanded)}
        className={`w-full text-left px-2 py-1 flex items-center gap-1.5 ${hasDetail ? "hover:bg-gray-100/50 cursor-pointer" : ""} transition-colors`}
      >
        <span className="text-gray-400 text-[10px] font-mono w-4 text-right shrink-0">{index + 1}</span>
        <Icon className="w-3 h-3 text-gray-500 shrink-0" />
        <span className="text-xs font-medium text-gray-700">{entry.toolName}</span>
        {!expanded && desc && <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>}
        {isLatest && <span className="text-[10px] text-amber-600 ml-auto shrink-0">current</span>}
        {hasDetail && (
          <svg className={`w-3 h-3 text-gray-400 shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4 2l4 4-4 4" /></svg>
        )}
      </button>
      {expanded && (
        <div className="px-2 pb-1.5 pt-0.5 border-t border-gray-200">
          {desc && <div className="text-xs text-gray-600 break-all">{desc}</div>}
          {entry.input != null && <ToolInputView input={entry.input} />}
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
            {task.startedAt && (
              <span className="text-[10px] text-gray-400 shrink-0">{new Date(task.startedAt).toLocaleTimeString()}</span>
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
          {task.startedAt && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Started At</span>
              <div className="text-gray-700 text-xs mt-0.5">{new Date(task.startedAt).toLocaleString()}</div>
            </div>
          )}
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

const COLLAPSE_THRESHOLD = 4;
const VISIBLE_WHEN_COLLAPSED = 3;

export function ActiveTasksPanel({ tasks, sessionId }: { tasks: ActiveTask[]; sessionId?: string }) {
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(new Set());
  // Track previous task snapshots to detect task_progress updates for hidden tasks
  const prevTasksRef = useRef<Map<string, ActiveTask>>(new Map());
  const [expanded, setExpanded] = useState(false);

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

  // Reset expanded state when tasks drop below threshold
  const visibleTasks = tasks.filter((t) => !hiddenIds.has(t.taskId));
  useEffect(() => {
    if (visibleTasks.length < COLLAPSE_THRESHOLD) {
      setExpanded(false);
    }
  }, [visibleTasks.length]);

  const handleDismiss = useCallback(async (taskId: string) => {
    setHiddenIds((prev) => new Set(prev).add(taskId));
    if (sessionId) {
      try {
        await api.dismissTask(sessionId, taskId);
      } catch (e) {
        console.error("Failed to dismiss task", e);
      }
    }
  }, [sessionId]);

  if (visibleTasks.length === 0) return null;

  const collapsible = visibleTasks.length >= COLLAPSE_THRESHOLD;
  const displayedTasks =
    collapsible && !expanded
      ? visibleTasks.slice(-VISIBLE_WHEN_COLLAPSED)
      : visibleTasks;


  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-amber-700">
        <span className="inline-block w-2 h-2 rounded-full bg-amber-500 animate-pulse" />
        Running Tasks ({visibleTasks.length})
      </div>
      {displayedTasks.map((task) => (
        <ActiveTaskItem key={task.taskId} task={task} onDismiss={handleDismiss} />
      ))}
      {collapsible && (
        <button
          type="button"
          onClick={() => setExpanded((prev) => !prev)}
          className="text-xs text-amber-600 hover:text-amber-800 hover:underline cursor-pointer"
        >
          {expanded
            ? `最新 ${VISIBLE_WHEN_COLLAPSED} 件のみ表示`
            : `他 ${visibleTasks.length - VISIBLE_WHEN_COLLAPSED} 件を表示`}
        </button>
      )}
    </div>
  );
}
