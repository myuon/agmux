import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LogEntry } from "../api/client";

const levelColors: Record<string, string> = {
  DEBUG: "text-gray-400",
  INFO: "text-blue-600",
  WARN: "text-yellow-600",
  ERROR: "text-red-600",
};

export function LogPanel() {
  const [logs, setLogs] = useState<LogEntry[]>([]);

  useEffect(() => {
    const load = () => api.getLogs(200).then(setLogs).catch(() => {});
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-2 border-b border-gray-700 bg-gray-800 rounded-t-lg">
        <h2 className="text-sm font-semibold text-gray-200">Daemon Logs</h2>
        <span className="text-xs text-gray-400">{logs.length} entries</span>
      </div>
      <div className="bg-gray-900 rounded-b-lg p-3 font-mono text-xs overflow-auto flex-1 min-h-0">
        {logs.length === 0 ? (
          <p className="text-gray-500">No logs yet</p>
        ) : (
          <table className="w-full">
            <tbody>
              {[...logs].reverse().map((log, i) => (
                <tr key={i} className="align-top hover:bg-gray-800">
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
                    {log.session && (
                      <span className="text-cyan-400 ml-1">{log.session}</span>
                    )}
                  </td>
                  <td className="text-gray-100 py-0.5 break-all">
                    {log.msg}
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
