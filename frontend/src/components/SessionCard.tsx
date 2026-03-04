import type { Session } from "../types/session";

const statusColors: Record<Session["status"], string> = {
  running: "bg-green-100 text-green-800",
  waiting: "bg-yellow-100 text-yellow-800",
  error: "bg-red-100 text-red-800",
  done: "bg-blue-100 text-blue-800",
  stopped: "bg-gray-100 text-gray-800",
};

const statusDots: Record<Session["status"], string> = {
  running: "bg-green-500",
  waiting: "bg-yellow-500",
  error: "bg-red-500",
  done: "bg-blue-500",
  stopped: "bg-gray-400",
};

interface Props {
  session: Session;
  onStop: (id: string) => void;
  onSelect: (id: string) => void;
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export function SessionCard({ session, onStop, onSelect }: Props) {
  return (
    <div className="border border-gray-200 rounded-lg p-4 hover:shadow-md transition-shadow">
      <div className="flex items-center justify-between mb-2">
        <h3 className="font-semibold text-lg">{session.name}</h3>
        <span
          className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium ${statusColors[session.status]}`}
        >
          <span
            className={`w-2 h-2 rounded-full ${statusDots[session.status]}`}
          />
          {session.status}
        </span>
      </div>
      <p className="text-sm text-gray-500 truncate mb-1">
        {session.projectPath}
      </p>
      <p className="text-xs text-gray-400 mb-3">
        {timeAgo(session.createdAt)}
      </p>
      <div className="flex gap-2">
        {(session.status === "running" || session.status === "waiting") && (
          <button
            onClick={() => onStop(session.id)}
            className="px-3 py-1 text-sm bg-red-50 text-red-600 rounded hover:bg-red-100"
          >
            Stop
          </button>
        )}
        <button
          onClick={() => onSelect(session.id)}
          className="px-3 py-1 text-sm bg-gray-50 text-gray-700 rounded hover:bg-gray-100"
        >
          View
        </button>
      </div>
    </div>
  );
}
