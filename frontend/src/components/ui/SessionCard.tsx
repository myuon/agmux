import type { Session } from "../../types/session";
import { StatusDot } from "../StatusBadge";
import { Chip } from "./Chip";

interface SessionCardProps {
  id?: string;
  name: string;
  status: Session["status"];
  type?: string;
  provider?: string;
  roleTemplate?: string;
  currentTask?: string;
  lastError?: string;
  projectPath: string;
  timeAgo: string;
  isSubSession?: boolean;
  isSelected?: boolean;
  onClick?: () => void;
  actions?: React.ReactNode;
  completionReport?: string;
}

/**
 * Short label for the project directory. Hidden for auto-created temporary
 * workspaces (~/.agmux/workspaces/...) since the path carries no meaning.
 */
function projectLabel(projectPath: string): string | null {
  if (!projectPath) return null;
  if (projectPath.includes("/.agmux/workspaces/")) return null;
  const parts = projectPath.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || null;
}

export function SessionCard({
  id,
  name,
  status,
  type,
  provider,
  roleTemplate,
  currentTask,
  lastError,
  projectPath,
  timeAgo,
  isSubSession,
  isSelected,
  onClick,
  actions,
  completionReport,
}: SessionCardProps) {
  const vtn = (suffix: string) => id ? { viewTransitionName: `session-${suffix}-${id}` } : undefined;
  const project = type === "controller" ? null : projectLabel(projectPath);
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick?.(); } }}
      className={`text-left w-full border rounded-lg px-3 py-2 transition-shadow cursor-pointer ${isSelected ? "bg-blue-50 border-blue-400 shadow-sm" : "bg-white hover:shadow-sm"} ${isSubSession && !isSelected ? "border-blue-200 border-l-blue-400 border-l-2" : ""} ${isSubSession && isSelected ? "border-l-2" : ""} ${!isSubSession && !isSelected ? "border-gray-200" : ""}`}
    >
      <div className="flex items-center gap-2">
        <span className="inline-flex shrink-0" style={vtn("dot")}><StatusDot status={status} /></span>
        <span className="font-medium text-sm truncate" style={vtn("name")}>
          {name}
        </span>
        {type === "controller" && (
          <Chip color="purple">Controller</Chip>
        )}
        {type === "ephemeral" && (
          <Chip color="blue">Ephemeral</Chip>
        )}
        {roleTemplate && (
          <span className="inline-flex items-center" style={vtn("role")}><Chip color="orange">{roleTemplate}</Chip></span>
        )}
        {provider && provider !== "claude" && (
          <Chip color={provider === "codex" ? "green" : "gray"}>
            {provider.charAt(0).toUpperCase() + provider.slice(1)}
          </Chip>
        )}
        <span className="text-xs text-gray-400 ml-auto shrink-0 flex items-center gap-1.5">
          <span style={vtn("status")}>{status}</span>
          <span>·</span>
          <span>{timeAgo}</span>
        </span>
      </div>
      {(project || currentTask) && (
        <p className="text-xs truncate mt-0.5">
          {project && (
            <span className="text-gray-400" title={projectPath}>{project}</span>
          )}
          {project && currentTask && <span className="text-gray-300"> · </span>}
          {currentTask && <span className="text-indigo-600">{currentTask}</span>}
        </p>
      )}
      {status === "exited" && lastError && (
        <p className="text-xs text-red-600 truncate mt-0.5" title={lastError}>
          Error: {lastError}
        </p>
      )}
      {status === "archived" && completionReport && (
        <p className="text-xs text-green-700 truncate mt-0.5" title={completionReport}>
          {completionReport}
        </p>
      )}
      {actions && (
        <div className="flex justify-end gap-1.5 mt-1">
          {actions}
        </div>
      )}
    </div>
  );
}
