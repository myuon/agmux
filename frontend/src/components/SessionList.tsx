import { useNavigate } from "react-router-dom";
import type { Session } from "../types/session";

const statusDots: Record<Session["status"], string> = {
  working: "bg-green-500",
  idle: "bg-blue-500",
  question_waiting: "bg-orange-500",
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
  onRestartController: () => void;
}

export function SessionList({ sessions, onStop, onRestartController }: Props) {
  const navigate = useNavigate();

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
          onClick={() => navigate(`/sessions/${s.id}`)}
          className="border border-gray-200 rounded-lg p-3 hover:shadow-sm transition-shadow bg-white cursor-pointer"
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
              {(s.status === "working" || s.status === "idle" || s.status === "question_waiting") && (
                <button
                  onClick={(e) => { e.stopPropagation(); onStop(s.id); }}
                  className="px-2 py-0.5 text-xs bg-red-50 text-red-600 rounded hover:bg-red-100"
                >
                  Stop
                </button>
              )}
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
      ))}
    </div>
  );
}
