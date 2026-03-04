import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LogEntry } from "../api/client";

const levelColors: Record<string, string> = {
  DEBUG: "text-gray-400",
  INFO: "text-blue-600",
  WARN: "text-yellow-600",
  ERROR: "text-red-600",
};

export function LogViewer({ onBack }: { onBack: () => void }) {
  const [logs, setLogs] = useState<LogEntry[]>([]);

  useEffect(() => {
    const load = () => api.getLogs(200).then(setLogs).catch(() => {});
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-8 py-4 flex items-center gap-4">
        <button
          onClick={onBack}
          className="text-sm text-gray-500 hover:text-gray-800"
        >
          &larr; Back
        </button>
        <h1 className="text-xl font-bold text-gray-900">Daemon Logs</h1>
        <span className="text-sm text-gray-400">{logs.length} entries</span>
      </header>

      <main className="p-4 max-w-7xl mx-auto">
        <div className="bg-gray-900 rounded-lg p-4 font-mono text-xs overflow-auto max-h-[80vh]">
          {logs.length === 0 ? (
            <p className="text-gray-500">No logs yet</p>
          ) : (
            <table className="w-full">
              <tbody>
                {[...logs].reverse().map((log, i) => (
                  <tr key={i} className="align-top hover:bg-gray-800">
                    <td className="text-gray-500 pr-3 whitespace-nowrap py-0.5">
                      {new Date(log.time).toLocaleTimeString()}
                    </td>
                    <td
                      className={`pr-3 whitespace-nowrap font-semibold py-0.5 ${levelColors[log.level] || "text-gray-400"}`}
                    >
                      {log.level}
                    </td>
                    <td className="text-gray-300 pr-3 whitespace-nowrap py-0.5">
                      {log.component && (
                        <span className="text-purple-400">
                          [{log.component}]
                        </span>
                      )}
                      {log.session && (
                        <span className="text-cyan-400 ml-1">
                          {log.session}
                        </span>
                      )}
                    </td>
                    <td className="text-gray-100 py-0.5">{log.msg}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </main>
    </div>
  );
}
