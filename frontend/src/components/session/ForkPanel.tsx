import { useState, useEffect, useCallback, useRef } from "react";
import { Link } from "react-router-dom";
import { ChevronDown, ChevronUp, X, GitBranch } from "lucide-react";
import type { Session } from "../../types/session";
import { api } from "../../api/client";
import { StatusDot } from "../StatusBadge";
import { StreamOutputView } from "./StreamOutputView";
import { useWebSocket } from "../../hooks/useWebSocket";

function ForkStreamPanel({ fork }: { fork: Session }) {
  const [streamLines, setStreamLines] = useState<unknown[]>([]);
  const [partialText, setPartialText] = useState("");
  const [forkSession, setForkSession] = useState<Session>(fork);

  useEffect(() => {
    api.getStreamOutput(fork.id).then((r) => {
      setStreamLines(r.lines);
    }).catch(() => {});
  }, [fork.id]);

  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "stream_update") {
      const data = msg.data as { sessionId: string; lines: unknown[]; total: number };
      if (data.sessionId === fork.id && data.lines.length > 0) {
        const regular: unknown[] = [];
        for (const line of data.lines) {
          const entry = line as Record<string, unknown>;
          if (entry.type === "stream_event") {
            const evt = entry.event as { type?: string; delta?: { type?: string; text?: string } } | undefined;
            if (evt?.type === "content_block_delta" && evt.delta?.type === "text_delta" && evt.delta.text) {
              setPartialText((prev) => prev + evt.delta!.text!);
            }
          } else {
            if (entry.type === "assistant") {
              setPartialText("");
            }
            regular.push(line);
          }
        }
        if (regular.length > 0) {
          setStreamLines((prev) => [...prev, ...regular]);
        }
      }
    }
    if (msg.type === "status_change") {
      const data = msg.data as { sessionId: string; status: Session["status"] };
      if (data.sessionId === fork.id) {
        setForkSession((prev) => ({ ...prev, status: data.status }));
      }
    }
  }, [fork.id]);

  useWebSocket(handleWsMessage);

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-100 shrink-0">
        <StatusDot status={forkSession.status} />
        <Link
          to={`/sessions/${fork.id}`}
          className="text-sm font-medium text-blue-600 hover:underline truncate"
        >
          {fork.name}
        </Link>
        <span className="text-xs text-gray-400 ml-auto shrink-0">{forkSession.status}</span>
      </div>
      <StreamOutputView
        lines={streamLines}
        partialText={partialText}
        className="flex-1 min-h-0"
        sessionId={fork.id}
        provider={fork.provider ?? undefined}
      />
    </div>
  );
}

export function ForkPanel({ sessionId }: { sessionId: string }) {
  const [forks, setForks] = useState<Session[]>([]);
  const [selectedForkId, setSelectedForkId] = useState<string | null>(null);
  const [minimized, setMinimized] = useState(false);
  const isMobile = typeof window !== "undefined" && window.innerWidth < 768;
  const [showMobileOverlay, setShowMobileOverlay] = useState(false);
  const prevForksRef = useRef<Session[]>([]);

  useEffect(() => {
    api.listForkSessions(sessionId).then((forkList) => {
      setForks(forkList);
      if (forkList.length > 0 && !selectedForkId) {
        setSelectedForkId(forkList[0].id);
      }
    }).catch(() => {});

    const interval = setInterval(() => {
      api.listForkSessions(sessionId).then((forkList) => {
        const prevIds = prevForksRef.current.map((f) => f.id);
        const newIds = forkList.map((f) => f.id);
        const hasNew = newIds.some((id) => !prevIds.includes(id));
        setForks(forkList);
        if (hasNew && forkList.length > 0) {
          setSelectedForkId(forkList[forkList.length - 1].id);
        }
      }).catch(() => {});
    }, 5000);

    return () => clearInterval(interval);
  }, [sessionId, selectedForkId]);

  useEffect(() => {
    prevForksRef.current = forks;
  }, [forks]);

  if (forks.length === 0) return null;

  const selectedFork = forks.find((f) => f.id === selectedForkId) ?? forks[0];

  // Mobile: show as button that opens overlay
  if (isMobile) {
    return (
      <>
        <div className="shrink-0 border-t border-gray-200 bg-white px-4 py-2">
          <button
            onClick={() => setShowMobileOverlay(true)}
            className="flex items-center gap-2 text-sm text-blue-600 font-medium"
          >
            <GitBranch className="w-4 h-4" />
            Forks ({forks.length})
          </button>
        </div>

        {showMobileOverlay && (
          <div className="fixed inset-0 z-50 bg-white flex flex-col">
            <div className="flex items-center gap-2 px-4 py-3 border-b border-gray-200 shrink-0">
              <button onClick={() => setShowMobileOverlay(false)} className="text-gray-500 hover:text-gray-700">
                <X className="w-5 h-5" />
              </button>
              <span className="font-semibold text-sm">Forks</span>
              <div className="flex gap-1 ml-4 overflow-x-auto">
                {forks.map((fork) => (
                  <button
                    key={fork.id}
                    onClick={() => setSelectedForkId(fork.id)}
                    className={`flex items-center gap-1.5 px-2 py-1 rounded text-xs shrink-0 ${
                      fork.id === selectedForkId
                        ? "bg-blue-100 text-blue-700"
                        : "text-gray-600 hover:bg-gray-100"
                    }`}
                  >
                    <StatusDot status={fork.status} />
                    <span className="max-w-[120px] truncate">{fork.name}</span>
                  </button>
                ))}
              </div>
            </div>
            <div className="flex-1 min-h-0">
              {selectedFork && <ForkStreamPanel key={selectedFork.id} fork={selectedFork} />}
            </div>
          </div>
        )}
      </>
    );
  }

  // Desktop: nested panel at bottom
  return (
    <div className={`shrink-0 border-t border-gray-200 bg-gray-50 transition-all ${minimized ? "h-10" : "h-72"}`}>
      {/* Panel header with tabs */}
      <div className="flex items-center gap-1 px-3 h-10 border-b border-gray-200 bg-white overflow-x-auto">
        <GitBranch className="w-3.5 h-3.5 text-gray-400 shrink-0" />
        <span className="text-xs text-gray-500 font-medium shrink-0 mr-2">Forks</span>
        {forks.map((fork) => (
          <button
            key={fork.id}
            onClick={() => { setSelectedForkId(fork.id); setMinimized(false); }}
            className={`flex items-center gap-1.5 px-2 py-1 rounded text-xs shrink-0 transition-colors ${
              fork.id === selectedForkId && !minimized
                ? "bg-blue-100 text-blue-700"
                : "text-gray-600 hover:bg-gray-100"
            }`}
          >
            <StatusDot status={fork.status} />
            <span className="max-w-[140px] truncate">{fork.name}</span>
          </button>
        ))}
        <div className="ml-auto flex items-center gap-1 shrink-0">
          <button
            onClick={() => setMinimized((v) => !v)}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
            title={minimized ? "Expand" : "Minimize"}
          >
            {minimized ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
          </button>
        </div>
      </div>

      {/* Panel content */}
      {!minimized && (
        <div className="h-[calc(100%-40px)]">
          {selectedFork && <ForkStreamPanel key={selectedFork.id} fork={selectedFork} />}
        </div>
      )}
    </div>
  );
}
