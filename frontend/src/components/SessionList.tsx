import type { Session } from "../types/session";

const statusDots: Record<Session["status"], string> = {
  running: "bg-green-500",
  waiting: "bg-yellow-500",
  error: "bg-red-500",
  done: "bg-blue-500",
  stopped: "bg-gray-400",
};

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

interface Props {
  sessions: Session[];
  onStop: (id: string) => void;
  onDelete: (id: string) => void;
  onSelect: (id: string) => void;
  onRestartController: () => void;
}

export function SessionList({ sessions, onStop, onDelete, onSelect, onRestartController }: Props) {
  if (sessions.length === 0) {
    return (
      <div className="text-center text-gray-400 py-8">
        <p className="text-sm">No sessions yet</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      {sessions.map((s) => (
        <div
          key={s.id}
          className="border border-gray-200 rounded-lg p-3 hover:shadow-sm transition-shadow bg-white"
        >
          <div className="flex items-center gap-2 mb-1">
            <span className={`w-2 h-2 rounded-full shrink-0 ${statusDots[s.status]}`} />
            <span className="font-medium text-sm truncate">{s.name}</span>
            {s.type === "controller" && (
              <span className="px-1.5 py-0.5 text-[10px] font-medium bg-purple-100 text-purple-700 rounded">
                Controller
              </span>
            )}
            <span className="text-xs text-gray-400 ml-auto shrink-0">
              {s.status}
            </span>
          </div>
          <p className="text-xs text-gray-500 truncate mb-1">
            {s.projectPath}
          </p>
          <div className="flex items-center justify-between">
            <span className="text-xs text-gray-400">
              {timeAgo(s.createdAt)}
            </span>
            <div className="flex gap-1.5">
              {(s.status === "running" || s.status === "waiting") && (
                <button
                  onClick={() => onStop(s.id)}
                  className="px-2 py-0.5 text-xs bg-red-50 text-red-600 rounded hover:bg-red-100"
                >
                  Stop
                </button>
              )}
              {s.type === "controller" && (s.status === "stopped" || s.status === "done" || s.status === "error") && (
                <button
                  onClick={() => onRestartController()}
                  className="px-2 py-0.5 text-xs bg-purple-50 text-purple-600 rounded hover:bg-purple-100"
                >
                  Restart
                </button>
              )}
              {s.type !== "controller" && (
                <button
                  onClick={() => onDelete(s.id)}
                  className="px-2 py-0.5 text-xs bg-gray-50 text-gray-500 rounded hover:bg-gray-100"
                >
                  Delete
                </button>
              )}
              <button
                onClick={() => onSelect(s.id)}
                className="px-2 py-0.5 text-xs bg-gray-50 text-gray-700 rounded hover:bg-gray-100"
              >
                View
              </button>
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}
