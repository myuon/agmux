import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { NotificationEntry } from "../api/client";
import { useWebSocket } from "../hooks/useWebSocket";

function relativeTime(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffSec = Math.floor((now - then) / 1000);

  if (diffSec < 60) return "just now";
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h ago`;
  const diffDay = Math.floor(diffHour / 24);
  return `${diffDay}d ago`;
}

function kindConfig(kind: string) {
  if (kind === "escalation") {
    return { color: "bg-orange-400", dotColor: "bg-orange-400", label: "Escalation", textColor: "text-orange-600" };
  }
  if (kind === "system") {
    return { color: "bg-gray-400", dotColor: "bg-gray-400", label: "System", textColor: "text-gray-500" };
  }
  return { color: "bg-blue-400", dotColor: "bg-blue-400", label: "Notification", textColor: "text-blue-600" };
}

export function NotificationPanel() {
  const [notifications, setNotifications] = useState<NotificationEntry[]>([]);
  const navigate = useNavigate();

  const load = useCallback(() => {
    api.getNotifications(100).then(setNotifications).catch(() => {});
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleWsMessage = useCallback(
    (msg: { type: string }) => {
      if (msg.type === "agent_notification" || msg.type === "escalation" || msg.type === "notify") {
        load();
      }
    },
    [load]
  );

  useWebSocket(handleWsMessage);

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-2.5 border-b border-gray-200 bg-white rounded-t-lg">
        <h2 className="text-sm font-semibold text-gray-800">Notifications</h2>
        <span className="text-xs text-gray-400 bg-gray-100 rounded-full px-2 py-0.5">{notifications.length}</span>
      </div>
      <div className="bg-white rounded-b-lg overflow-auto flex-1 min-h-0">
        {notifications.length === 0 ? (
          <p className="text-sm text-gray-400 px-4 py-6 text-center">No notifications yet</p>
        ) : (
          <ul className="divide-y divide-gray-100">
            {notifications.map((n) => {
              const config = kindConfig(n.kind);
              return (
                <li
                  key={n.id}
                  className={`flex items-start gap-3 px-4 py-2.5 hover:bg-gray-50 transition-colors ${
                    n.sessionId ? "cursor-pointer" : ""
                  }`}
                  onClick={n.sessionId ? () => navigate(`/sessions/${n.sessionId}`) : undefined}
                >
                  {/* Color bar / dot */}
                  <div className="flex-shrink-0 pt-1.5">
                    <div className={`w-2 h-2 rounded-full ${config.dotColor}`} />
                  </div>

                  {/* Content */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={`text-xs font-medium ${config.textColor}`}>{config.label}</span>
                      {n.sessionId && (
                        <span className="text-xs text-gray-400">
                          {n.sessionName || n.sessionId.slice(0, 8)}
                        </span>
                      )}
                    </div>
                    <p className="text-sm text-gray-700 leading-snug mt-0.5 break-words line-clamp-2">
                      {n.message}
                    </p>
                  </div>

                  {/* Relative time */}
                  <span className="flex-shrink-0 text-xs text-gray-400 pt-0.5 whitespace-nowrap">
                    {relativeTime(n.createdAt)}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
