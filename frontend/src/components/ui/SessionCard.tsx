import type { Session } from "../../types/session";
import { StatusDot } from "../StatusBadge";
import { Chip } from "./Chip";

interface SessionCardProps {
  name: string;
  status: Session["status"];
  type?: string;
  provider?: string;
  currentTask?: string;
  lastError?: string;
  projectPath: string;
  timeAgo: string;
  isSubSession?: boolean;
  onClick?: () => void;
  actions?: React.ReactNode;
}

export function SessionCard({
  name,
  status,
  type,
  provider,
  currentTask,
  lastError,
  projectPath,
  timeAgo,
  isSubSession,
  onClick,
  actions,
}: SessionCardProps) {
  return (
    <div
      onClick={onClick}
      className={`border rounded-lg p-3 transition-shadow bg-white hover:shadow-sm cursor-pointer ${isSubSession ? "border-blue-200 border-l-blue-400 border-l-2" : "border-gray-200"}`}
    >
      <div className="flex items-center gap-2 mb-1">
        <StatusDot status={status} />
        <span className="font-medium text-sm truncate">
          {name}
        </span>
        {type === "controller" && (
          <Chip color="purple">Controller</Chip>
        )}
        {provider && provider !== "claude" && (
          <Chip color={provider === "codex" ? "green" : "gray"}>
            {provider.charAt(0).toUpperCase() + provider.slice(1)}
          </Chip>
        )}
        <span className="text-xs text-gray-400 ml-auto shrink-0">
          {status}
        </span>
      </div>
      {currentTask && (
        <p className="text-xs text-indigo-600 truncate mb-0.5">{currentTask}</p>
      )}
      {status === "exited" && lastError && (
        <p className="text-xs text-red-600 truncate mb-0.5" title={lastError}>
          Error: {lastError}
        </p>
      )}
      <p className="text-xs text-gray-500 truncate mb-1">
        {projectPath}
      </p>
      <div className="flex items-center justify-between">
        <span className="text-xs text-gray-400">
          {timeAgo}
        </span>
        {actions && (
          <div className="flex gap-1.5">
            {actions}
          </div>
        )}
      </div>
    </div>
  );
}
