import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { NotificationEntry } from "../api/client";
import { useWebSocket } from "../hooks/useWebSocket";

export function NotificationPanel() {
  const [notifications, setNotifications] = useState<NotificationEntry[]>([]);
  const navigate = useNavigate();

  const load = useCallback(() => {
    api.getNotifications(100).then(setNotifications).catch(() => {});
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // Reload on new notification via WebSocket
  const handleWsMessage = useCallback(
    (msg: { type: string }) => {
      if (msg.type === "agent_notification" || msg.type === "escalation") {
        load();
      }
    },
    [load]
  );

  useWebSocket(handleWsMessage);

  const kindLabel = (kind: string) => {
    if (kind === "escalation") return "Escalation";
    return "Notification";
  };

  const kindColor = (kind: string) => {
    if (kind === "escalation") return "text-orange-400";
    return "text-blue-400";
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-2 border-b border-gray-700 bg-gray-800 rounded-t-lg">
        <h2 className="text-sm font-semibold text-gray-200">Notifications</h2>
        <span className="text-xs text-gray-400">{notifications.length}</span>
      </div>
      <div className="bg-gray-900 rounded-b-lg p-3 font-mono text-xs overflow-auto flex-1 min-h-0">
        {notifications.length === 0 ? (
          <p className="text-gray-500">No notifications yet</p>
        ) : (
          <table className="w-full">
            <tbody>
              {notifications.map((n) => (
                <tr key={n.id} className="align-top hover:bg-gray-800">
                  <td className="text-gray-500 pr-2 whitespace-nowrap py-0.5">
                    {new Date(n.createdAt + "Z").toLocaleTimeString()}
                  </td>
                  <td className={`pr-2 whitespace-nowrap py-0.5 ${kindColor(n.kind)}`}>
                    {kindLabel(n.kind)}
                  </td>
                  <td className="text-gray-300 pr-2 whitespace-nowrap py-0.5">
                    {n.sessionId ? (
                      <button
                        onClick={() => navigate(`/sessions/${n.sessionId}`)}
                        className="text-cyan-400 underline hover:text-cyan-300 cursor-pointer"
                      >
                        {n.sessionName || n.sessionId.slice(0, 8)}
                      </button>
                    ) : null}
                  </td>
                  <td className="text-gray-100 py-0.5 break-all">
                    {n.message}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
