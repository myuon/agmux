import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { TerminalSquare } from "lucide-react";
import type { Session } from "../types/session";
import { StatusDot } from "./StatusBadge";
import { GroupSectionHeader } from "./ui/GroupSectionHeader";

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function projectDisplayName(projectPath: string): string {
  if (!projectPath) return "Unknown Project";
  const parts = projectPath.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || projectPath;
}

function groupSessionsByProject(sessions: Session[]): Map<string, Session[]> {
  const groups = new Map<string, Session[]>();
  for (const session of sessions) {
    const key = session.projectPath || "";
    const group = groups.get(key);
    if (group) {
      group.push(session);
    } else {
      groups.set(key, [session]);
    }
  }
  return groups;
}

interface Props {
  sessions: Session[];
  onRestartController: () => void;
}

export function SessionList({ sessions, onRestartController }: Props) {
  const navigate = useNavigate();
  const groupedSessions = useMemo(() => {
    const groups = groupSessionsByProject(sessions);
    // Sort: controller group first
    const entries = [...groups.entries()].sort(([, a], [, b]) => {
      const aHasController = a.some(s => s.type === "controller");
      const bHasController = b.some(s => s.type === "controller");
      if (aHasController && !bHasController) return -1;
      if (!aHasController && bHasController) return 1;
      return 0;
    });
    return entries;
  }, [sessions]);

  if (sessions.length === 0) {
    return (
      <div className="text-center text-gray-400 py-8">
        <p className="text-sm">No sessions yet</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {groupedSessions.map(([projectPath, groupSessions]) => {
        const isController = groupSessions.some(s => s.type === "controller");
        return (
        <div key={projectPath}>
          <GroupSectionHeader
            icon={isController ? <TerminalSquare className="w-3.5 h-3.5 text-purple-500" /> : undefined}
            title={projectDisplayName(projectPath)}
            count={groupSessions.length}
          />
          <div className="flex flex-col gap-2">
            {groupSessions.map((s) =>
              s.type === "external" ? (
                <div
                  key={s.id}
                  className="flex items-center gap-2 px-3 py-1.5 rounded border border-dashed border-amber-300 bg-amber-50/50 text-xs text-gray-500"
                >
                  <span className="inline-block w-1.5 h-1.5 rounded-full bg-amber-400 shrink-0" />
                  <span className="font-medium text-amber-700 shrink-0">{s.provider === "codex" ? "Codex" : s.provider === "claude" ? "Claude" : "External"}</span>
                  <span className="text-gray-600 truncate">{s.name}</span>
                  <span className="font-mono text-gray-400 shrink-0">
                    PID {s.id.replace("ext-", "")}
                  </span>
                  <span className="text-gray-400 ml-auto shrink-0">{timeAgo(s.createdAt)}</span>
                </div>
              ) : (
                <div
                  key={s.id}
                  onClick={() => navigate(`/sessions/${s.id}`)}
                  className="border border-gray-200 rounded-lg p-3 transition-shadow bg-white hover:shadow-sm cursor-pointer"
                >
                  <div className="flex items-center gap-2 mb-1">
                    <StatusDot status={s.status} />
                    <span className="font-medium text-sm truncate">{s.name}</span>
                    {s.type === "controller" && (
                      <span className="px-1.5 py-0.5 text-[10px] font-medium bg-purple-100 text-purple-700 rounded">
                        Controller
                      </span>
                    )}
                    {s.provider && s.provider !== "claude" && (
                      <span className={`px-1.5 py-0.5 text-[10px] font-medium rounded ${
                        s.provider === "codex"
                          ? "bg-green-100 text-green-700"
                          : "bg-gray-100 text-gray-600"
                      }`}>
                        {s.provider.charAt(0).toUpperCase() + s.provider.slice(1)}
                      </span>
                    )}

                    <span className="text-xs text-gray-400 ml-auto shrink-0">
                      {s.status}
                    </span>
                  </div>
                  {s.currentTask && (
                    <p className="text-xs text-indigo-600 truncate mb-0.5">{s.currentTask}</p>
                  )}
                  <p className="text-xs text-gray-500 truncate mb-1">
                    {s.projectPath}
                  </p>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-gray-400">
                      {timeAgo(s.createdAt)}
                    </span>
                    <div className="flex gap-1.5">
                      {s.type === "controller" && s.status === "stopped" && (
                        <button
                          onClick={(e) => { e.stopPropagation(); onRestartController(); }}
                          className="px-2 py-0.5 text-xs bg-purple-50 text-purple-600 rounded hover:bg-purple-100"
                        >
                          Restart
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              )
            )}
          </div>
        </div>
        );
      })}
    </div>
  );
}
