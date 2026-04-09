import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useState } from "react";
import { Outlet, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "./api/client";
import type { Session } from "./types/session";
import { NotificationPanel } from "./components/NotificationPanel";
import { SessionList } from "./components/SessionList";
import { useWebSocket } from "./hooks/useWebSocket";
import { getActiveSessionName } from "./activeSession";
import { IconButton } from "./components/ui/IconButton";
import { CreateSession } from "./components/CreateSession";
import { BroadcastModal } from "./components/BroadcastModal";

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

type MobileTab = "sessions" | "notifications";

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
    if (msg.type === "agent_notification") {
      const data = msg.data as { sessionId: string; sessionName: string; message: string };
      sendNotification("agmux - Notification", `${data.sessionName}: ${data.message}`, data.sessionId);
      return;
    }
    if (msg.type === "notify") {
      const notify = localStorage.getItem("agmux-notify") === "true";
      if (!notify) return;
      const data = msg.data as { sessionId: string; sessionName: string; status: string; summary: string };
      const defaultStatuses: Record<string, boolean> = {
        working: false, idle: true, waiting_input: true,
        paused: false, exited: false,
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

// Hook to detect desktop screen (>=1024px)
export function useIsDesktop() {
  const [isDesktop, setIsDesktop] = useState(() => window.innerWidth >= 1024);

  useLayoutEffect(() => {
    const mq = window.matchMedia("(min-width: 1024px)");
    setIsDesktop(mq.matches);
    const handler = (e: MediaQueryListEvent) => setIsDesktop(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);

  return isDesktop;
}

// Context to signal that a page component is rendered inside the desktop center pane
export const DesktopPaneContext = createContext(false);
export function useDesktopPane() {
  return useContext(DesktopPaneContext);
}

function ChevronLeftIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
      <path fillRule="evenodd" d="M12.707 5.293a1 1 0 010 1.414L9.414 10l3.293 3.293a1 1 0 01-1.414 1.414l-4-4a1 1 0 010-1.414l4-4a1 1 0 011.414 0z" clipRule="evenodd" />
    </svg>
  );
}

function ChevronRightIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
      <path fillRule="evenodd" d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" clipRule="evenodd" />
    </svg>
  );
}

// Shared header action buttons
function AppHeaderButtons({
  sessions,
  devMode,
  onCreateSession,
  onBroadcast,
}: {
  sessions: Session[];
  devMode: boolean;
  onCreateSession: () => void;
  onBroadcast: () => void;
}) {
  const navigate = useNavigate();
  return (
    <div className="flex items-center gap-2">
      <button
        onClick={onCreateSession}
        className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
        title="New Session"
      >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
          <path fillRule="evenodd" d="M10 3a1 1 0 011 1v5h5a1 1 0 110 2h-5v5a1 1 0 11-2 0v-5H4a1 1 0 110-2h5V4a1 1 0 011-1z" clipRule="evenodd" />
        </svg>
      </button>
      {devMode && (
        <IconButton shape="rounded" to="/preview" title="UI Preview">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M4 3a2 2 0 00-2 2v10a2 2 0 002 2h12a2 2 0 002-2V5a2 2 0 00-2-2H4zm12 12H4l4-8 3 6 2-4 3 6z" clipRule="evenodd" />
          </svg>
        </IconButton>
      )}
      {devMode && (
        <IconButton shape="rounded" to="/scenarios" title="Scenario Test">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M6 2a2 2 0 00-2 2v12a2 2 0 002 2h8a2 2 0 002-2V7.414A2 2 0 0015.414 6L12 2.586A2 2 0 0010.586 2H6zm5 6a1 1 0 10-2 0v3.586l-1.293-1.293a1 1 0 10-1.414 1.414l3 3a1 1 0 001.414 0l3-3a1 1 0 00-1.414-1.414L11 11.586V8z" clipRule="evenodd" />
          </svg>
        </IconButton>
      )}
      <IconButton shape="rounded" to="/metrics" title="Metrics">
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
          <path d="M2 11a1 1 0 011-1h2a1 1 0 011 1v5a1 1 0 01-1 1H3a1 1 0 01-1-1v-5zM8 7a1 1 0 011-1h2a1 1 0 011 1v9a1 1 0 01-1 1H9a1 1 0 01-1-1V7zM14 4a1 1 0 011-1h2a1 1 0 011 1v12a1 1 0 01-1 1h-2a1 1 0 01-1-1V4z" />
        </svg>
      </IconButton>
      <IconButton shape="rounded" to="/config" title="Settings">
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
          <path fillRule="evenodd" d="M11.49 3.17c-.38-1.56-2.6-1.56-2.98 0a1.532 1.532 0 01-2.286.948c-1.372-.836-2.942.734-2.106 2.106.54.886.061 2.042-.947 2.287-1.561.379-1.561 2.6 0 2.978a1.532 1.532 0 01.947 2.287c-.836 1.372.734 2.942 2.106 2.106a1.532 1.532 0 012.287.947c.379 1.561 2.6 1.561 2.978 0a1.533 1.533 0 012.287-.947c1.372.836 2.942-.734 2.106-2.106a1.533 1.533 0 01.947-2.287c1.561-.379 1.561-2.6 0-2.978a1.532 1.532 0 01-.947-2.287c.836-1.372-.734-2.942-2.106-2.106a1.532 1.532 0 01-2.287-.947zM10 13a3 3 0 100-6 3 3 0 000 6z" clipRule="evenodd" />
        </svg>
      </IconButton>
      <button
        onClick={onBroadcast}
        className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100"
        title="Broadcast"
      >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4.9 19.1C1 15.2 1 8.8 4.9 4.9" />
          <path d="M7.8 16.2c-2.3-2.3-2.3-6.1 0-8.4" />
          <circle cx="12" cy="12" r="2" />
          <path d="M16.2 7.8c2.3 2.3 2.3 6.1 0 8.4" />
          <path d="M19.1 4.9C23 8.8 23 15.1 19.1 19" />
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
  );
}

// Hook to manage sessions state with WebSocket updates
function useSessions() {
  const [sessions, setSessions] = useState<Session[]>([]);

  const loadSessions = useCallback(() => {
    api.listSessions().then((data) => {
      setSessions(data);
    }).catch(() => {});
  }, []);

  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "session_update") {
      const newSessions = msg.data as Session[];
      setSessions(newSessions);
    }
    if (msg.type === "status_change") {
      const data = msg.data as { sessionId: string; status: Session["status"]; lastError?: string };
      setSessions(prev => prev.map(s =>
        s.id === data.sessionId
          ? { ...s, status: data.status, lastError: data.lastError ?? s.lastError }
          : s
      ));
    }
  }, []);

  useWebSocket(handleWsMessage);

  return { sessions, loadSessions };
}

// Desktop 3-pane layout: left (session list) + center (outlet) + right (notifications)
export function DesktopLayout() {
  const { sessions, loadSessions } = useSessions();
  const [error, setError] = useState<string | null>(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [broadcastOpen, setBroadcastOpen] = useState(false);
  const [devMode, setDevMode] = useState(false);
  const [leftCollapsed, setLeftCollapsed] = useState(() => {
    return localStorage.getItem("agmux-desktop-left-collapsed") === "true";
  });
  const [rightCollapsed, setRightCollapsed] = useState(() => {
    return localStorage.getItem("agmux-desktop-right-collapsed") === "true";
  });
  const navigate = useNavigate();

  useEffect(() => {
    api.getConfig().then((cfg) => setDevMode(cfg.devMode)).catch(() => {});
  }, []);

  const handleRestartController = async () => {
    try {
      await api.restartController();
      loadSessions();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to restart controller");
    }
  };

  const toggleLeft = () => {
    setLeftCollapsed(prev => {
      const next = !prev;
      localStorage.setItem("agmux-desktop-left-collapsed", String(next));
      return next;
    });
  };

  const toggleRight = () => {
    setRightCollapsed(prev => {
      const next = !prev;
      localStorage.setItem("agmux-desktop-right-collapsed", String(next));
      return next;
    });
  };

  return (
    <div className="h-screen flex flex-col bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200 px-4 py-3 flex items-center justify-between shrink-0">
        <h1 className="text-lg font-bold text-gray-900">agmux</h1>
        <AppHeaderButtons
          sessions={sessions}
          devMode={devMode}
          onCreateSession={() => setShowCreateModal(true)}
          onBroadcast={() => setBroadcastOpen(true)}
        />
      </header>

      <BroadcastModal open={broadcastOpen} onClose={() => setBroadcastOpen(false)} sessions={sessions} />

      {/* Error banner */}
      {error && (
        <div className="bg-red-50 border-b border-red-200 text-red-700 px-4 py-2 text-sm shrink-0 flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="ml-2 text-red-500 hover:text-red-800">x</button>
        </div>
      )}

      {/* 3-pane body */}
      <div className="flex-1 min-h-0 flex overflow-hidden">
        {/* Left pane: session list */}
        {!leftCollapsed ? (
          <div className="w-72 shrink-0 border-r border-gray-200 bg-white flex flex-col min-h-0">
            <div className="flex items-center justify-between px-4 py-2.5 border-b border-gray-100 shrink-0">
              <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide">
                Sessions ({sessions.length})
              </h2>
              <button
                onClick={toggleLeft}
                className="p-1 text-gray-400 hover:text-gray-600 rounded hover:bg-gray-100"
                title="Collapse session list"
              >
                <ChevronLeftIcon />
              </button>
            </div>
            <div className="flex-1 min-h-0 overflow-y-auto p-3">
              <SessionList sessions={sessions} onRestartController={handleRestartController} />
            </div>
          </div>
        ) : (
          <div className="w-8 shrink-0 border-r border-gray-200 bg-white flex flex-col items-center py-3">
            <button
              onClick={toggleLeft}
              className="p-1 text-gray-400 hover:text-gray-600 rounded hover:bg-gray-100"
              title="Expand session list"
            >
              <ChevronRightIcon />
            </button>
          </div>
        )}

        {/* Center pane: outlet */}
        <div className="flex-1 min-h-0 overflow-hidden flex flex-col">
          <DesktopPaneContext.Provider value={true}>
            <Outlet />
          </DesktopPaneContext.Provider>
        </div>

        {/* Right pane: notifications */}
        {!rightCollapsed ? (
          <div className="w-72 shrink-0 border-l border-gray-200 bg-white flex flex-col min-h-0">
            <div className="flex-1 min-h-0 overflow-hidden flex flex-col">
              <NotificationPanel
                headerExtra={
                  <button
                    onClick={toggleRight}
                    className="p-1 text-gray-400 hover:text-gray-600 rounded hover:bg-gray-100"
                    title="Collapse notifications"
                  >
                    <ChevronRightIcon />
                  </button>
                }
              />
            </div>
          </div>
        ) : (
          <div className="w-8 shrink-0 border-l border-gray-200 bg-white flex flex-col items-center py-3">
            <button
              onClick={toggleRight}
              className="p-1 text-gray-400 hover:text-gray-600 rounded hover:bg-gray-100"
              title="Expand notifications"
            >
              <ChevronLeftIcon />
            </button>
          </div>
        )}
      </div>

      {showCreateModal && (
        <CreateSession
          onClose={() => setShowCreateModal(false)}
          onCreate={async (data) => {
            try {
              const created = await api.createSession(data);
              setShowCreateModal(false);
              navigate(`/sessions/${created.id}`);
            } catch (e: unknown) {
              setError(e instanceof Error ? e.message : "Failed to create session");
            }
          }}
        />
      )}
    </div>
  );
}

// Mobile Dashboard — session list + notifications with tab switcher
export function Dashboard() {
  const { sessions, loadSessions } = useSessions();
  const [error, setError] = useState<string | null>(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [broadcastOpen, setBroadcastOpen] = useState(false);
  const [devMode, setDevMode] = useState(false);
  const [searchParams, setSearchParams] = useSearchParams();
  const mobileTab: MobileTab = (searchParams.get("tab") as MobileTab) || "sessions";
  const navigate = useNavigate();

  useEffect(() => {
    api.getConfig().then((cfg) => setDevMode(cfg.devMode)).catch(() => {});
  }, []);

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
        <AppHeaderButtons
          sessions={sessions}
          devMode={devMode}
          onCreateSession={() => setShowCreateModal(true)}
          onBroadcast={() => setBroadcastOpen(true)}
        />
      </header>
      <BroadcastModal open={broadcastOpen} onClose={() => setBroadcastOpen(false)} sessions={sessions} />

      {/* Error banner */}
      {error && (
        <div className="bg-red-50 border-b border-red-200 text-red-700 px-4 py-2 text-sm shrink-0 flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="ml-2 text-red-500 hover:text-red-800">x</button>
        </div>
      )}

      {/* Mobile tab switcher */}
      <div className="flex border-b border-gray-200">
        <button
          className={`flex-1 py-2 text-sm font-medium ${
            mobileTab === "sessions" ? "text-blue-600 border-b-2 border-blue-600" : "text-gray-500"
          }`}
          onClick={() => setSearchParams({})}
        >
          Sessions ({sessions.length})
        </button>
        <button
          className={`flex-1 py-2 text-sm font-medium ${
            mobileTab === "notifications" ? "text-blue-600 border-b-2 border-blue-600" : "text-gray-500"
          }`}
          onClick={() => setSearchParams({ tab: "notifications" })}
        >
          Notifications
        </button>
      </div>

      {/* Main content */}
      <div className="flex-1 min-h-0 flex flex-col">
        <div className={`flex-1 min-h-0 overflow-y-auto p-3 ${mobileTab === "sessions" ? "block" : "hidden"}`}>
          <SessionList sessions={sessions} onRestartController={handleRestartController} />
        </div>
        <div className={`min-h-0 p-3 ${mobileTab === "notifications" ? "flex-1 flex flex-col" : "hidden"}`}>
          <div className="flex-1 min-h-0 flex flex-col">
            <NotificationPanel />
          </div>
        </div>
      </div>

      {showCreateModal && (
        <CreateSession
          onClose={() => setShowCreateModal(false)}
          onCreate={async (data) => {
            try {
              const created = await api.createSession(data);
              setShowCreateModal(false);
              navigate(`/sessions/${created.id}`);
            } catch (e: unknown) {
              setError(e instanceof Error ? e.message : "Failed to create session");
            }
          }}
        />
      )}
    </div>
  );
}

// Desktop empty state shown in center pane when no session is selected
export function DesktopEmptyState() {
  return (
    <div className="h-full flex flex-col items-center justify-center text-gray-400">
      <svg xmlns="http://www.w3.org/2000/svg" className="h-12 w-12 mb-3 text-gray-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
        <line x1="3" y1="9" x2="21" y2="9" />
        <line x1="9" y1="21" x2="9" y2="9" />
      </svg>
      <p className="text-sm">Select a session to get started</p>
    </div>
  );
}

// Home page: on desktop shows empty state (layout has session list/notifications in panes),
// on mobile shows the full Dashboard
export function HomePage() {
  const isDesktop = useIsDesktop();
  if (isDesktop) {
    return <DesktopEmptyState />;
  }
  return <Dashboard />;
}

// Root layout switcher: on desktop wraps in DesktopLayout, on mobile just passes through
export function AppShell() {
  const isDesktop = useIsDesktop();
  if (isDesktop) {
    return <DesktopLayout />;
  }
  return <Outlet />;
}

// Root App component: provides global notifications
function App() {
  useGlobalNotifications();
  return <Outlet />;
}

export default App;
