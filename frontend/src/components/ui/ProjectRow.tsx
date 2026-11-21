import { useState } from "react";
import type { AutomationSummary, ProjectDetail } from "../../api/client";
import { ProjectSummaryLine } from "../ProjectSummaryLine";
import { LoopRow } from "./LoopRow";
import { statusDots } from "../StatusBadge";

interface ProjectRowProps {
  project: ProjectDetail;
  /** Loop summaries of this project's automations (experimental "loops"). */
  loops: AutomationSummary[];
  loopsOn: boolean;
  /** Last update of the whole project (coordinator, members, automation runs). */
  updatedAt: string | null;
  isSelected: boolean;
  timeAgo: (dateStr: string) => string;
  onOpenProject: () => void;
  onOpenLoop: (id: string) => void;
  onOpenSession: (id: string) => void;
}


function dotClass(status: string): string {
  const base = (statusDots as Record<string, string>)[status] ?? "bg-gray-300";
  return status === "working" ? `${base} animate-pulse` : base;
}

/** One project in the session list: the coordinator is the main character. */
export function ProjectRow({
  project,
  loops,
  loopsOn,
  updatedAt,
  isSelected,
  timeAgo,
  onOpenProject,
  onOpenLoop,
  onOpenSession,
}: ProjectRowProps) {
  const [open, setOpen] = useState(false);
  const [showArchived, setShowArchived] = useState(false);
  const coord = project.coordinator;
  const coordId = coord?.id;
  const subs = project.sessions.filter((s) => s.id !== coordId && s.status !== "archived");
  const archived = project.archivedSessions ?? [];
  const loopIds = new Set(loops.map((l) => l.id));
  // Automations that have no loop summary (loops off, or hidden as noise) get a plain row.
  const plainAutos = project.automationList.filter((a) => !loopIds.has(a.id));
  const hasChildren = subs.length > 0 || archived.length > 0 || loops.length > 0 || plainAutos.length > 0;
  const alert = project.pendingEscalations > 0 || project.automations.stopped > 0;
  return (
    <div>
      <div
        role="button"
        tabIndex={0}
        onClick={onOpenProject}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpenProject();
          }
        }}
        className={`w-full border rounded-lg px-3 py-2 cursor-pointer bg-white hover:shadow-sm ${
          isSelected ? "border-blue-400 ring-1 ring-blue-200" : alert ? "border-orange-200" : "border-gray-200"
        }`}
      >
        <div className="flex items-center gap-2">
          <span
            className={`w-2 h-2 rounded-full shrink-0 ${coord ? (dotClass(coord.status)) : "bg-gray-300"}`}
          />
          <span className="text-gray-500 text-sm shrink-0" title="Project">
            ▣
          </span>
          <span className="font-medium text-sm truncate min-w-0">{project.name}</span>
          <span className="ml-auto shrink-0 text-xs text-gray-400">{updatedAt ? timeAgo(updatedAt) : ""}</span>
          {hasChildren && (
            <button
              type="button"
              aria-label={open ? "配下を閉じる" : "配下を開く"}
              aria-expanded={open}
              onClick={(e) => {
                e.stopPropagation();
                setOpen((v) => !v);
              }}
              className="shrink-0 px-1.5 text-gray-400 hover:text-gray-600 text-xs"
            >
              {open ? "▾" : "▸"}
            </button>
          )}
        </div>
        <div className="mt-0.5 pl-4 text-xs text-gray-500 truncate">
          {coord ? coord.name || coord.id : "コーディネーターなし"}
        </div>
        <div className="mt-0.5 pl-4 text-xs text-gray-500">
          <ProjectSummaryLine project={project} loops={loopsOn} />
        </div>
      </div>
      {open && (
        <div className="mt-1 ml-3 pl-2 border-l border-gray-200 flex flex-col gap-1">
          {loops.map((l) => (
            <LoopRow key={l.id} loop={l} timeAgo={timeAgo} onOpenLoop={onOpenLoop} onOpenSession={onOpenSession} />
          ))}
          {plainAutos.map((a) => (
            <button
              key={a.id}
              type="button"
              onClick={() => onOpenLoop(a.id)}
              className="w-full flex items-center gap-2 text-left text-xs border border-gray-200 rounded-lg bg-white px-3 py-1.5 hover:shadow-sm"
            >
              <span className="text-purple-600">↻</span>
              <span className="truncate min-w-0">{a.name}</span>
              <span className={`ml-auto shrink-0 ${a.state === "stopped" ? "text-red-600" : "text-gray-500"}`}>
                {a.state === "stopped" ? "停止" : a.state === "running" ? "実行中" : "待機中"}
              </span>
            </button>
          ))}
          {subs.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => onOpenSession(s.id)}
              className="w-full flex items-center gap-2 text-left text-xs border border-gray-200 rounded-lg bg-white px-3 py-1.5 hover:shadow-sm"
            >
              <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${dotClass(s.status)}`} />
              <span className="truncate min-w-0">{s.name || s.id}</span>
              <span className="ml-auto shrink-0 text-gray-400">{timeAgo(s.updatedAt)}</span>
            </button>
          ))}
          {archived.length > 0 && (
            <>
              <button
                type="button"
                aria-expanded={showArchived}
                onClick={() => setShowArchived((v) => !v)}
                className="text-left text-xs text-gray-400 hover:text-gray-600 px-1 py-0.5"
              >
                {showArchived ? "▾" : "▸"} アーカイブ済み {archived.length} 件
              </button>
              {showArchived &&
                archived.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    onClick={() => onOpenSession(s.id)}
                    className="w-full flex items-center gap-2 text-left text-xs border border-gray-100 rounded-lg bg-gray-50 px-3 py-1.5 text-gray-500 hover:shadow-sm"
                  >
                    <span className="w-1.5 h-1.5 rounded-full shrink-0 bg-gray-300" />
                    <span className="truncate min-w-0">{s.name || s.id}</span>
                    <span className="ml-auto shrink-0 text-gray-400">{timeAgo(s.updatedAt)}</span>
                  </button>
                ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}
