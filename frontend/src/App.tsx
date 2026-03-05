import { useCallback, useEffect, useRef, useState } from "react";
import { Routes, Route, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "./api/client";
import type { Session } from "./types/session";
import { CreateSession } from "./components/CreateSession";
import { SessionDetail } from "./components/SessionDetail";
import { LogPanel } from "./components/LogPanel";
import { SessionList } from "./components/SessionList";
import { ConfigPage } from "./components/ConfigPage";
import { useWebSocket } from "./hooks/useWebSocket";

function sendNotification(title: string, body: string) {
  if ("Notification" in window && Notification.permission === "granted") {
    new Notification(title, { body });
  }
}

type MobileTab = "logs" | "sessions";

// Global notification hook — runs regardless of which page is active
function useGlobalNotifications() {
  const prevSessionsRef = useRef<Map<string, string>>(new Map());

  useEffect(() => {
    // Seed prevSessionsRef with current session statuses
    api.listSessions().then((data) => {
      const map = new Map<string, string>();
      for (const s of data) map.set(s.id, s.status);
      prevSessionsRef.current = map;
    });
  }, []);

  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "session_update") {
      const newSessions = msg.data as Session[];
      const prev = prevSessionsRef.current;
      const notify = localStorage.getItem("agmux-notify") === "true";
      if (notify) {
        for (const s of newSessions) {
          const prevStatus = prev.get(s.id);
          if (s.status === "question_waiting" && prevStatus && prevStatus !== "question_waiting") {
            sendNotification("agmux", `${s.name}: ユーザーの入力を待っています`);
          }
          if (s.status === "alignment_needed" && prevStatus && prevStatus !== "alignment_needed") {
            sendNotification("agmux", `${s.name}: ユーザーとのアラインメントが必要です`);
          }
        }
      }
      const map = new Map<string, string>();
      for (const s of newSessions) map.set(s.id, s.status);
      prevSessionsRef.current = map;
    }
  }, []);

  useWebSocket(handleWsMessage);
}

function Dashboard() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [showCreate, setShowCreate] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [searchParams, setSearchParams] = useSearchParams();
  const mobileTab: MobileTab = searchParams.get("tab") === "sessions" ? "sessions" : "logs";
  const navigate = useNavigate();
  const [notifyEnabled, setNotifyEnabled] = useState(() => {
    return localStorage.getItem("agmux-notify") === "true";
  });

  const toggleNotify = async () => {
    if (!notifyEnabled) {
      if ("Notification" in window) {
        const perm = Notification.permission === "granted"
          ? "granted"
          : await Notification.requestPermission();
        if (perm === "granted") {
          setNotifyEnabled(true);
          localStorage.setItem("agmux-notify", "true");
        }
      }
    } else {
      setNotifyEnabled(false);
      localStorage.setItem("agmux-notify", "false");
    }
  };

  const loadSessions = () => {
    api.listSessions().then((data) => {
      setSessions(data);
    }).catch((e) => setError(e.message));
  };

  useEffect(() => {
    loadSessions();
  }, []);

  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "session_update") {
      const newSessions = msg.data as Session[];
      setSessions(newSessions);
    }
  }, []);

  useWebSocket(handleWsMessage);

  const handleCreate = async (data: {
    name: string;
    projectPath: string;
    prompt?: string;
    outputMode?: "terminal" | "stream";
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

  const handleRestartController = async () => {
    try {
      await api.restartController();
      loadSessions();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to restart controller");
    }
  };

  return (
    <div className="h-screen flex flex-col bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200 px-4 md:px-8 py-3 flex items-center justify-between shrink-0">
        <h1 className="text-lg md:text-xl font-bold text-gray-900">agmux</h1>
        <div className="flex items-center gap-2">
          <button
            onClick={toggleNotify}
            className={`p-1.5 rounded-lg hover:bg-gray-100 ${notifyEnabled ? "text-blue-600" : "text-gray-400"}`}
            title={notifyEnabled ? "Notifications ON" : "Notifications OFF"}
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              {notifyEnabled ? (
                <path d="M10 2a6 6 0 00-6 6v3.586l-.707.707A1 1 0 004 14h12a1 1 0 00.707-1.707L16 11.586V8a6 6 0 00-6-6zM10 18a3 3 0 01-3-3h6a3 3 0 01-3 3z" />
              ) : (
                <path fillRule="evenodd" d="M3.707 2.293a1 1 0 00-1.414 1.414l14 14a1 1 0 001.414-1.414l-1.473-1.473A6.002 6.002 0 0016 8a6 6 0 00-6-6c-1.558 0-2.98.594-4.048 1.566L3.707 2.293zM4 8c0-.31.024-.615.07-.912L13.586 16.6H4a1 1 0 01-.707-1.707L4 14.186V8zm6 10a3 3 0 01-2.83-2h5.66A3 3 0 0110 18z" clipRule="evenodd" />
              )}
            </svg>
          </button>
          <button
            onClick={() => navigate("/config")}
            className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
            title="Settings"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M11.49 3.17c-.38-1.56-2.6-1.56-2.98 0a1.532 1.532 0 01-2.286.948c-1.372-.836-2.942.734-2.106 2.106.54.886.061 2.042-.947 2.287-1.561.379-1.561 2.6 0 2.978a1.532 1.532 0 01.947 2.287c-.836 1.372.734 2.942 2.106 2.106a1.532 1.532 0 012.287.947c.379 1.561 2.6 1.561 2.978 0a1.533 1.533 0 012.287-.947c1.372.836 2.942-.734 2.106-2.106a1.533 1.533 0 01.947-2.287c1.561-.379 1.561-2.6 0-2.978a1.532 1.532 0 01-.947-2.287c.836-1.372-.734-2.942-2.106-2.106a1.532 1.532 0 01-2.287-.947zM10 13a3 3 0 100-6 3 3 0 000 6z" clipRule="evenodd" />
            </svg>
          </button>
          <button
            onClick={() => setShowCreate(true)}
            className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700"
          >
            + New Session
          </button>
        </div>
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
          onClick={() => setSearchParams({})}
          className={`flex-1 py-2.5 text-sm font-medium text-center ${
            mobileTab === "logs"
              ? "text-blue-600 border-b-2 border-blue-600"
              : "text-gray-500"
          }`}
        >
          Logs
        </button>
        <button
          onClick={() => setSearchParams({ tab: "sessions" })}
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
            onRestartController={handleRestartController}
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

function App() {
  useGlobalNotifications();

  return (
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/sessions/:id" element={<SessionDetail />} />
      <Route path="/config" element={<ConfigPage />} />
    </Routes>
  );
}

export default App;
