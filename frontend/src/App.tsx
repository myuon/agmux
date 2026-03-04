import { useCallback, useEffect, useState } from "react";
import { api } from "./api/client";
import type { Session } from "./types/session";
import { CreateSession } from "./components/CreateSession";
import { SessionDetail } from "./components/SessionDetail";
import { LogPanel } from "./components/LogPanel";
import { SessionList } from "./components/SessionList";
import { useWebSocket } from "./hooks/useWebSocket";

type MobileTab = "logs" | "sessions";

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [showCreate, setShowCreate] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mobileTab, setMobileTab] = useState<MobileTab>("logs");

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
    <div className="h-screen flex flex-col bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200 px-4 md:px-8 py-3 flex items-center justify-between shrink-0">
        <h1 className="text-lg md:text-xl font-bold text-gray-900">agmux</h1>
        <button
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700"
        >
          + New Session
        </button>
      </header>

      {/* Error banner */}
      {error && (
        <div className="bg-red-50 border-b border-red-200 text-red-700 px-4 py-2 text-sm shrink-0 flex items-center justify-between">
          <span>{error}</span>
          <button
            onClick={() => setError(null)}
            className="ml-2 text-red-500 hover:text-red-800"
          >
            x
          </button>
        </div>
      )}

      {/* Mobile tab switcher */}
      <div className="md:hidden flex border-b border-gray-200 bg-white shrink-0">
        <button
          onClick={() => setMobileTab("logs")}
          className={`flex-1 py-2.5 text-sm font-medium text-center ${
            mobileTab === "logs"
              ? "text-blue-600 border-b-2 border-blue-600"
              : "text-gray-500"
          }`}
        >
          Logs
        </button>
        <button
          onClick={() => setMobileTab("sessions")}
          className={`flex-1 py-2.5 text-sm font-medium text-center ${
            mobileTab === "sessions"
              ? "text-blue-600 border-b-2 border-blue-600"
              : "text-gray-500"
          }`}
        >
          Sessions ({sessions.length})
        </button>
      </div>

      {/* Main content */}
      <div className="flex-1 min-h-0 flex flex-col md:flex-row">
        {/* Log panel - desktop: always visible, mobile: only when tab active */}
        <div
          className={`flex-1 min-h-0 p-3 md:p-4 ${
            mobileTab === "logs" ? "flex flex-col" : "hidden md:flex md:flex-col"
          }`}
        >
          <LogPanel />
        </div>

        {/* Session sidebar - desktop: always visible, mobile: only when tab active */}
        <div
          className={`md:w-80 md:border-l border-gray-200 bg-white overflow-y-auto p-3 md:p-4 ${
            mobileTab === "sessions" ? "flex-1" : "hidden md:block"
          }`}
        >
          <h2 className="text-sm font-semibold text-gray-700 mb-3 hidden md:block">
            Active Sessions ({sessions.length})
          </h2>
          <SessionList
            sessions={sessions}
            onStop={handleStop}
            onSelect={setSelectedId}
          />
        </div>
      </div>

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
