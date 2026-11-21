import { useState } from "react";
import type { AutomationSummary } from "../../api/client";
import { Chip } from "./Chip";
import { lampClass } from "../StatusBadge";

interface LoopRowProps {
  loop: AutomationSummary;
  /** Loops that add tasks to this one, rendered hanging below the row. */
  feeders?: AutomationSummary[];
  nested?: boolean;
  timeAgo: (dateStr: string) => string;
  onOpenLoop: (id: string) => void;
  onOpenSession: (id: string) => void;
}

const stateLabel = { running: "実行中", waiting: "待機中", stopped: "停止" } as const;
const stateChip = { running: "blue", waiting: "gray", stopped: "red" } as const;
const stateDot = {
  running: "bg-blue-500 animate-pulse",
  waiting: "bg-gray-400",
  stopped: "bg-red-400",
} as const;

function formatNext(dateStr: string): string {
  const d = new Date(dateStr);
  const diff = d.getTime() - Date.now();
  if (diff <= 60_000) return "まもなく";
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return diff < 24 * 3600_000 ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

/** Strip the "disabled: " prefix of a stop reason for a short display. */
function shortReason(reason: string): string {
  return reason.replace(/^disabled:\s*/, "");
}

export function LoopRow({ loop, feeders = [], nested, timeAgo, onOpenLoop, onOpenSession }: LoopRowProps) {
  const [open, setOpen] = useState(false);
  const next =
    loop.state === "stopped"
      ? null
      : loop.nextFireAt
        ? `次回 ${formatNext(loop.nextFireAt)}`
        : loop.nextReason || null;
  return (
    <div className={nested ? "ml-5" : ""}>
      <div
        role="button"
        tabIndex={0}
        onClick={() => onOpenLoop(loop.id)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpenLoop(loop.id);
          }
        }}
        className={`w-full border rounded-lg px-3 py-2 cursor-pointer bg-white hover:shadow-sm ${nested ? "border-purple-200 border-l-2 border-l-purple-400" : "border-gray-200"}`}
      >
        <div className="flex items-center gap-2">
          <span className={`w-2 h-2 rounded-full shrink-0 ${stateDot[loop.state]}`} />
          <span className="text-purple-600 text-sm shrink-0" title="Loop">{nested ? "←" : ""}↻</span>
          <span className="font-medium text-sm truncate min-w-0">{loop.name}</span>
          <button
            type="button"
            aria-label={open ? "各回を閉じる" : "各回を開く"}
            aria-expanded={open}
            onClick={(e) => {
              e.stopPropagation();
              setOpen((v) => !v);
            }}
            className="ml-auto shrink-0 px-1.5 text-gray-400 hover:text-gray-600 text-xs"
          >
            {open ? "▾" : "▸"}
          </button>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-gray-500 pl-4">
          <Chip color={stateChip[loop.state]}>{stateLabel[loop.state]}</Chip>
          {loop.state === "stopped" && loop.stoppedReason && (
            <span className="text-red-500 truncate max-w-full">{shortReason(loop.stoppedReason)}</span>
          )}
          <span>残タスク {loop.pendingTasks}</span>
          {next && <span>{next}</span>}
          <span>{loop.lastRunAt ? `最終 ${timeAgo(loop.lastRunAt)}` : "未実行"}</span>
        </div>
        {open && (
          <ul className="mt-1.5 pl-4 flex flex-col gap-0.5" onClick={(e) => e.stopPropagation()}>
            {loop.recentRuns.length === 0 && <li className="text-xs text-gray-400">まだ実行されていません</li>}
            {loop.recentRuns.map((r) => (
              <li key={r.runId}>
                <button
                  type="button"
                  onClick={() => onOpenSession(r.sessionId)}
                  className="w-full flex items-center gap-2 text-left text-xs text-gray-600 hover:bg-gray-50 rounded px-1 py-0.5"
                >
                  <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${lampClass(r.sessionStatus, r.sessionUnread)}`} />
                  <span className="truncate min-w-0">{r.sessionName || r.sessionId}</span>
                  <span className="ml-auto shrink-0 text-gray-400">{timeAgo(r.firedAt)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {feeders.length > 0 && (
        <div className="mt-1 flex flex-col gap-1">
          {feeders.map((f) => (
            <LoopRow key={f.id} loop={f} nested timeAgo={timeAgo} onOpenLoop={onOpenLoop} onOpenSession={onOpenSession} />
          ))}
        </div>
      )}
    </div>
  );
}
