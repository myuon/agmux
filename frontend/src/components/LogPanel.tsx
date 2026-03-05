import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { LogEntry } from "../api/client";

export function LogPanel() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const navigate = useNavigate();

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
        <span className="text-xs text-gray-400">{logs.length}</span>
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
                  <td className="text-gray-300 pr-2 whitespace-nowrap py-0.5">
                    {log.sessionId ? (
                      <button
                        onClick={() => navigate(`/sessions/${log.sessionId}`)}
                        className="text-cyan-400 underline hover:text-cyan-300 cursor-pointer"
                      >
                        {log.session || log.sessionId.slice(0, 8)}
                      </button>
                    ) : null}
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
