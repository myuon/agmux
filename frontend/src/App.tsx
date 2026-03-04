import { useCallback, useEffect, useState } from "react";
import { api } from "./api/client";
import type { Session } from "./types/session";
import { SessionCard } from "./components/SessionCard";
import { CreateSession } from "./components/CreateSession";
import { SessionDetail } from "./components/SessionDetail";
import { LogViewer } from "./components/LogViewer";
import { useWebSocket } from "./hooks/useWebSocket";

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [showCreate, setShowCreate] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showLogs, setShowLogs] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadSessions = () => {
    api.listSessions().then(setSessions).catch((e) => setError(e.message));
  };

  useEffect(() => {
    loadSessions();
  }, []);

  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "session_update") {
      setSessions(msg.data as Session[]);
    }
  }, []);

  useWebSocket(handleWsMessage);

  const handleCreate = async (data: {
    name: string;
    projectPath: string;
    prompt?: string;
  }) => {
    try {
      await api.createSession(data);
      setShowCreate(false);
      loadSessions();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create session");
    }
  };

  const handleStop = async (id: string) => {
    try {
      await api.stopSession(id);
      loadSessions();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to stop session");
    }
  };

  if (showLogs) {
    return <LogViewer onBack={() => setShowLogs(false)} />;
  }

  if (selectedId) {
    return (
      <SessionDetail
        sessionId={selectedId}
        onBack={() => {
          setSelectedId(null);
          loadSessions();
        }}
      />
    );
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-8 py-4 flex items-center justify-between">
        <h1 className="text-xl font-bold text-gray-900">agmux Dashboard</h1>
        <div className="flex gap-2">
          <button
            onClick={() => setShowLogs(true)}
            className="px-4 py-2 text-sm bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200"
          >
            Logs
          </button>
          <button
            onClick={() => setShowCreate(true)}
            className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700"
          >
            + New Session
          </button>
        </div>
      </header>

      <main className="p-8 max-w-6xl mx-auto">
        {error && (
          <div className="bg-red-50 border border-red-200 text-red-700 px-4 py-2 rounded mb-4 text-sm">
            {error}
            <button
              onClick={() => setError(null)}
              className="ml-2 text-red-500 hover:text-red-800"
            >
              x
            </button>
          </div>
        )}

        {sessions.length === 0 ? (
          <div className="text-center text-gray-400 py-20">
            <p className="text-lg">No sessions yet</p>
            <p className="text-sm mt-1">
              Create a new session to get started.
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {sessions.map((s) => (
              <SessionCard
                key={s.id}
                session={s}
                onStop={handleStop}
                onSelect={setSelectedId}
              />
            ))}
          </div>
        )}
      </main>

      {showCreate && (
        <CreateSession
          onClose={() => setShowCreate(false)}
          onCreate={handleCreate}
        />
      )}
    </div>
  );
}

export default App;
