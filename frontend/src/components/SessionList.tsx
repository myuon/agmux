import { useMemo } from "react";
import { useNavigate, useMatch } from "react-router-dom";
import { TerminalSquare } from "lucide-react";
import { motion } from "motion/react";
import type { Session } from "../types/session";
import { GroupSectionHeader } from "./ui/GroupSectionHeader";
import { SecondaryButton } from "./ui/SecondaryButton";
import { ExternalProcessRow } from "./ui/ExternalProcessRow";
import { SessionCard } from "./ui/SessionCard";

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

/** Build a map from parent session ID to its child sessions */
function buildChildrenMap(sessions: Session[]): Map<string, Session[]> {
  const map = new Map<string, Session[]>();
  for (const s of sessions) {
    if (s.parentSessionId) {
      const children = map.get(s.parentSessionId);
      if (children) {
        children.push(s);
      } else {
        map.set(s.parentSessionId, [s]);
      }
    }
  }
  return map;
}

interface Props {
  sessions: Session[];
  onRestartController: () => void;
}

export function SessionList({ sessions, onRestartController }: Props) {
  const navigate = useNavigate();
  const sessionMatch = useMatch("/sessions/:id");
  const selectedSessionId = sessionMatch?.params.id ?? null;
  const childrenMap = useMemo(() => buildChildrenMap(sessions), [sessions]);

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

  let sessionIndex = 0;
  const renderSession = (s: Session, depth: number) => {
    const children = childrenMap.get(s.id) || [];
    const idx = sessionIndex++;
    return (
      <motion.div
        key={s.id}
        initial={{ opacity: 0, y: 16 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ type: "spring", damping: 25, stiffness: 300, delay: idx * 0.04 }}
      >
        <div style={{ marginLeft: depth * 24 }}>
          {s.type === "external" ? (
            <ExternalProcessRow
              provider={s.provider}
              name={s.name}
              pid={s.id.replace("ext-", "")}
              timeAgo={timeAgo(s.createdAt)}
            />
          ) : (
            <SessionCard
              id={s.id}
              name={s.name}
              status={s.status}
              type={s.type}
              provider={s.provider}
              roleTemplate={s.roleTemplate}
              currentTask={s.currentTask}
              lastError={s.lastError}
              projectPath={s.projectPath}
              timeAgo={timeAgo(s.createdAt)}
              isSubSession={depth > 0}
              isSelected={s.id === selectedSessionId}
              onClick={() => navigate(`/sessions/${s.id}`, { viewTransition: true } as never)}
              actions={
                s.type === "controller" && (s.status === "paused" || s.status === "exited") ? (
                  <SecondaryButton
                    onClick={(e) => { e.stopPropagation(); onRestartController(); }}
                  >
                    Restart
                  </SecondaryButton>
                ) : undefined
              }
            />
          )}
        </div>
        {children.map((child) => renderSession(child, depth + 1))}
      </motion.div>
    );
  };

  return (
    <div className="flex flex-col gap-4">
      {groupedSessions.map(([projectPath, groupSessions]) => {
        const isController = groupSessions.some(s => s.type === "controller");
        // Only show top-level sessions (those without a parent, or whose parent is not in this group)
        const topLevelSessions = groupSessions.filter(s => !s.parentSessionId);
        return (
        <div key={projectPath}>
          <GroupSectionHeader
            icon={isController ? <TerminalSquare className="w-3.5 h-3.5 text-purple-500" /> : undefined}
            title={projectDisplayName(projectPath)}
            count={groupSessions.length}
          />
          <div className="flex flex-col gap-2">
            {topLevelSessions.map((s) => renderSession(s, 0))}
          </div>
        </div>
        );
      })}
    </div>
  );
}
