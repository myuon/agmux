import { useCallback, useEffect, useState } from "react";
import { Routes, Route, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "./api/client";
import type { Session } from "./types/session";
import { SessionPage } from "./pages/SessionPage";
import { LogPanel } from "./components/LogPanel";
import { SessionList } from "./components/SessionList";
import { ConfigPage } from "./pages/ConfigPage";
import { MetricsPage } from "./pages/MetricsPage";
import { useWebSocket } from "./hooks/useWebSocket";
import { getActiveSessionName } from "./activeSession";

// Register service worker for mobile notifications
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

async function sendNotification(title: string, body: string, sessionId?: string) {
  if (!("Notification" in window) || Notification.permission !== "granted") return;

  const data = sessionId ? { sessionId } : undefined;

  // Mobile Chrome requires Service Worker for notifications
  if ("serviceWorker" in navigator) {
    const reg = await navigator.serviceWorker.ready.catch(() => null);
    if (reg) {
      reg.showNotification(title, { body, data });
      return;
    }
  }
  // Desktop fallback
  const n = new Notification(title, { body });
  if (sessionId) {
    n.onclick = () => {
      window.focus();
      window.location.href = `/sessions/${sessionId}`;
    };
  }
}

type MobileTab = "sessions" | "logs";

// Global notification hook — runs regardless of which page is active
function useGlobalNotifications() {
  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "server_started") {
      sendNotification("agmux", "Server started");
      return;
    }
    if (msg.type === "escalation") {
      const data = msg.data as { sessionId: string; sessionName: string; message: string };
      sendNotification("agmux - Escalation", `${data.sessionName}: ${data.message}`, data.sessionId);
      return;
    }
    if (msg.type === "goal_completed") {
      const enabled = localStorage.getItem("agmux-notify-goal-completed") !== "false";
      if (!enabled) return;
      const data = msg.data as { sessionId: string; sessionName: string; currentTask: string; goal: string; durationMs: number };
      const thresholdMin = Number(localStorage.getItem("agmux-notify-goal-threshold-min") || "10");
      if (data.durationMs < thresholdMin * 60 * 1000) return;
      const durationMin = Math.round(data.durationMs / 60000);
      sendNotification("agmux - Task Completed", `${data.sessionName}: ${data.currentTask} (${durationMin}min)`, data.sessionId);
      return;
    }
    if (msg.type === "notify") {
      const notify = localStorage.getItem("agmux-notify") === "true";
      if (!notify) return;
      const data = msg.data as { sessionId: string; sessionName: string; status: string; summary: string };
      const defaultStatuses: Record<string, boolean> = {
        working: false, idle: true, question_waiting: true,
        alignment_needed: true, paused: false, stopped: false,
      };
      const saved = localStorage.getItem("agmux-notify-statuses");
      const statusFilters = saved ? JSON.parse(saved) as Record<string, boolean> : defaultStatuses;
      if (!(statusFilters[data.status] ?? defaultStatuses[data.status] ?? true)) return;
      // Suppress notification if the user is currently viewing this session
      if (data.sessionName === getActiveSessionName()) return;
      sendNotification("agmux", `${data.sessionName}: ${data.summary}`, data.sessionId);
    }
  }, []);

  useWebSocket(handleWsMessage);
}

function Dashboard() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [searchParams, setSearchParams] = useSearchParams();
  const mobileTab: MobileTab = searchParams.get("tab") === "logs" ? "logs" : "sessions";
  const navigate = useNavigate();
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
            onClick={() => navigate("/metrics")}
            className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
            title="Metrics"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path d="M2 11a1 1 0 011-1h2a1 1 0 011 1v5a1 1 0 01-1 1H3a1 1 0 01-1-1v-5zM8 7a1 1 0 011-1h2a1 1 0 011 1v9a1 1 0 01-1 1H9a1 1 0 01-1-1V7zM14 4a1 1 0 011-1h2a1 1 0 011 1v12a1 1 0 01-1 1h-2a1 1 0 01-1-1V4z" />
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
            onClick={() => {
              const controller = sessions.find((s) => s.type === "controller");
              if (controller) {
                navigate(`/sessions/${controller.id}`);
              }
            }}
            className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
            title="Controller"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 17 10 11 4 5" />
              <line x1="12" y1="19" x2="20" y2="19" />
            </svg>
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
            mobileTab === "sessions"
              ? "text-blue-600 border-b-2 border-blue-600"
              : "text-gray-500"
          }`}
        >
          Sessions ({sessions.length})
        </button>
        <button
          onClick={() => setSearchParams({ tab: "logs" })}
          className={`flex-1 py-2.5 text-sm font-medium text-center ${
            mobileTab === "logs"
              ? "text-blue-600 border-b-2 border-blue-600"
              : "text-gray-500"
          }`}
        >
          Logs
        </button>
      </div>

      {/* Main content */}
      <div className="flex-1 min-h-0 flex flex-col md:flex-row">
        {/* Session list - desktop: always visible (main area), mobile: only when tab active */}
        <div
          className={`flex-1 min-h-0 overflow-y-auto p-3 md:p-4 ${
            mobileTab === "sessions" ? "block" : "hidden md:block"
          }`}
        >
          <h2 className="text-sm font-semibold text-gray-700 mb-3 hidden md:block">
            Active Sessions ({sessions.length})
          </h2>
          <SessionList
            sessions={sessions}
            onRestartController={handleRestartController}
          />
        </div>

        {/* Log panel - desktop: always visible (sidebar), mobile: only when tab active */}
        <div
          className={`md:w-[480px] md:border-l border-gray-200 bg-white min-h-0 p-3 md:p-4 ${
            mobileTab === "logs" ? "flex-1 flex flex-col" : "hidden md:flex md:flex-col"
          }`}
        >
          <LogPanel />
        </div>
      </div>

    </div>
  );
}

function App() {
  useGlobalNotifications();

  return (
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/sessions/:id" element={<SessionPage />} />
      <Route path="/config" element={<ConfigPage />} />
      <Route path="/metrics" element={<MetricsPage />} />
    </Routes>
  );
}

export default App;
