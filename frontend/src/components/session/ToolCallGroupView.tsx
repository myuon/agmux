import { createElement, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { AlertTriangle, CheckCircle2, ChevronRight, Layers, Loader2 } from "lucide-react";
import type { StreamDisplayItem } from "../../models/stream";
import { toolDescription, toolIcon, summarizeToolCategories } from "../../models/tool";
import { RunningBadge } from "./ToolCallView";

type ToolCallItem = Extract<StreamDisplayItem, { kind: "tool_call" }>;

const expandTransition = { type: "spring" as const, damping: 25, stiffness: 300 };

// Collapsed summary row for 2+ consecutive tool calls. Expanding it renders the
// individual rows (renderItem) — each still opens its own input/output modal.
export function ToolCallGroupView({ items, toolCalls, sessionActive, toolProgress, renderItem }: {
  items: StreamDisplayItem[];
  toolCalls: ToolCallItem[];
  sessionActive?: boolean;
  toolProgress?: Record<string, number>;
  renderItem: (item: StreamDisplayItem, index: number) => ReactNode;
}) {
  const [expanded, setExpanded] = useState(false);
  const last = toolCalls[toolCalls.length - 1];
  const running = last.result === undefined && !!last.running && !!sessionActive;
  // A failed background task counts as an error of its (launching) tool call.
  const errorCount = toolCalls.filter((t) => t.isError || t.backgroundTask?.status === "failed").length;
  const bgRunningCount = toolCalls.filter((t) => t.backgroundTask?.status === "running").length;
  const count = toolCalls.length;

  let content: ReactNode;
  if (running) {
    const desc = toolDescription(last.name, last.input);
    content = (
      <>
        {createElement(toolIcon(last.name), { className: "w-3.5 h-3.5 text-gray-500 shrink-0" })}
        <span className="font-medium text-xs text-gray-800 shrink-0">{last.name}</span>
        {desc && <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>}
        <span className="text-[11px] text-gray-400 shrink-0">({count}件目)</span>
      </>
    );
  } else {
    content = (
      <>
        <Layers className="w-3.5 h-3.5 text-gray-500 shrink-0" />
        <span className="font-medium text-xs text-gray-800 shrink-0">{count}件のツール呼び出し</span>
        <span className="text-xs text-gray-400 truncate min-w-0">
          {summarizeToolCategories(toolCalls.map((t) => t.name))}
        </span>
      </>
    );
  }

  return (
    <div>
      <button
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <ChevronRight className={`w-3 h-3 text-gray-400 shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} />
          {content}
          {errorCount > 0 && (
            <span
              className={`flex items-center gap-0.5 text-[11px] text-red-500 shrink-0 ${running ? "" : "ml-auto"}`}
              title={`${errorCount}件のエラー`}
            >
              <AlertTriangle className="w-3 h-3" />
              {errorCount}
            </span>
          )}
          {bgRunningCount > 0 && (
            <span
              className={`flex items-center gap-1 text-[11px] text-blue-500 shrink-0 ${running || errorCount > 0 ? "" : "ml-auto"}`}
              title={`${bgRunningCount}件のバックグラウンドタスクが実行中`}
            >
              {sessionActive && <Loader2 className="w-3 h-3 animate-spin" />}
              BG {bgRunningCount}
            </span>
          )}
          {running ? (
            <RunningBadge
              key={last.toolUseId}
              reportedSeconds={last.toolUseId ? toolProgress?.[last.toolUseId] : undefined}
            />
          ) : errorCount === 0 && bgRunningCount === 0 && toolCalls.every((t) => t.result !== undefined) ? (
            <CheckCircle2 className="w-3 h-3 text-green-500 ml-auto shrink-0" />
          ) : null}
        </div>
      </button>
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div
            className="mt-1 ml-3 pl-2 border-l border-gray-200 space-y-1"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={expandTransition}
            style={{ overflow: "hidden" }}
          >
            {items.map((item, i) => renderItem(item, i))}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
