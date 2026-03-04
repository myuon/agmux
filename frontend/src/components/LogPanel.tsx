import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { LogEntry } from "../api/client";

const levelColors: Record<string, string> = {
  DEBUG: "text-gray-400",
  INFO: "text-blue-600",
  WARN: "text-yellow-600",
  ERROR: "text-red-600",
};

const actionBadgeColors: Record<string, string> = {
  approve: "bg-green-700 text-green-200",
  retry: "bg-yellow-700 text-yellow-200",
  escalate: "bg-red-700 text-red-200",
  none: "bg-gray-700 text-gray-300",
};

const defaultLevelFilter: Record<string, boolean> = {
  DEBUG: false,
  INFO: true,
  WARN: true,
  ERROR: true,
};

export function LogPanel() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [levelFilter, setLevelFilter] = useState<Record<string, boolean>>(defaultLevelFilter);
  const navigate = useNavigate();

  useEffect(() => {
    const load = () => api.getLogs(200).then(setLogs).catch(() => {});
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  const filteredLogs = logs.filter((log) => levelFilter[log.level] !== false);

  const toggleLevel = (level: string) => {
    setLevelFilter((prev) => ({ ...prev, [level]: !prev[level] }));
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-2 border-b border-gray-700 bg-gray-800 rounded-t-lg">
        <h2 className="text-sm font-semibold text-gray-200">Daemon Logs</h2>
        <div className="flex items-center gap-1">
          {["DEBUG", "INFO", "WARN", "ERROR"].map((level) => (
            <button
              key={level}
              onClick={() => toggleLevel(level)}
              className={`px-1.5 py-0.5 text-[10px] font-semibold rounded transition-colors ${
                levelFilter[level]
                  ? `${levelColors[level]} opacity-100 bg-gray-700`
                  : "text-gray-600 opacity-50 bg-gray-800"
              }`}
            >
              {level}
            </button>
          ))}
          <span className="text-xs text-gray-400 ml-2">{filteredLogs.length}</span>
        </div>
      </div>
      <div className="bg-gray-900 rounded-b-lg p-3 font-mono text-xs overflow-auto flex-1 min-h-0">
        {filteredLogs.length === 0 ? (
          <p className="text-gray-500">No logs yet</p>
        ) : (
          <table className="w-full">
            <tbody>
              {[...filteredLogs].reverse().map((log, i) => {
                const isAction = (log as Record<string, unknown>).category === "action";
                return (
                  <tr
                    key={i}
                    className={`align-top hover:bg-gray-800 ${
                      isAction ? "border-l-2 border-emerald-500" : ""
                    }`}
                  >
                    <td className="text-gray-500 pr-2 whitespace-nowrap py-0.5">
                      {new Date(log.time).toLocaleTimeString()}
                    </td>
                    <td
                      className={`pr-2 whitespace-nowrap font-semibold py-0.5 ${levelColors[log.level] || "text-gray-400"}`}
                    >
                      {log.level}
                    </td>
                    <td className="text-gray-300 pr-2 whitespace-nowrap py-0.5">
                      {log.component && (
                        <span className="text-purple-400">
                          [{log.component}]
                        </span>
                      )}
                      {log.session && log.sessionId ? (
                        <button
                          onClick={() => navigate(`/sessions/${log.sessionId}`)}
                          className="text-cyan-400 ml-1 underline hover:text-cyan-300 cursor-pointer"
                        >
                          {log.session}
                        </button>
                      ) : log.session ? (
                        <span className="text-cyan-400 ml-1">{log.session}</span>
                      ) : null}
                    </td>
                    <td className="text-gray-100 py-0.5 break-all">
                      <LogMessage log={log} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function LogMessage({ log }: { log: LogEntry }) {
  const extra = log as Record<string, unknown>;
  const action = extra.action as string | undefined;
  const reason = extra.reason as string | undefined;
  const previousStatus = extra.previousStatus as string | undefined;
  const newStatus = extra.newStatus as string | undefined;
  const error = extra.error as string | undefined;
  const actionType = extra.actionType as string | undefined;
  const detail = extra.detail as string | undefined;

  return (
    <>
      <span>{log.msg}</span>
      {action && (
        <span
          className={`ml-1.5 px-1 py-0.5 rounded text-[10px] font-semibold ${
            actionBadgeColors[action] || actionBadgeColors.none
          }`}
        >
          {action}
        </span>
      )}
      {actionType && (
        <span className="ml-1.5 text-emerald-400 font-semibold">
          [{actionType}]
        </span>
      )}
      {detail && (
        <span className="ml-1 text-gray-300">{detail}</span>
      )}
      {reason && (
        <span className="ml-1.5 italic text-gray-400">{reason}</span>
      )}
      {previousStatus && newStatus && (
        <span className="ml-1.5 text-orange-400">
          {previousStatus} → {newStatus}
        </span>
      )}
      {error && (
        <span className="ml-1.5 text-red-400">{error}</span>
      )}
    </>
  );
}
