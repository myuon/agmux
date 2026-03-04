import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import type { Session } from "../types/session";
import { api, type ClaudeLogEntry, type ClaudeContentBlock } from "../api/client";

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-900/30", label: "User", text: "text-blue-300" },
  assistant: { bg: "bg-gray-800/50", label: "Assistant", text: "text-green-300" },
};

function ContentBlockView({ block }: { block: ClaudeContentBlock }) {
  if (block.type === "text") {
    return (
      <div className="prose prose-invert prose-xs max-w-none prose-pre:bg-gray-950 prose-pre:text-gray-300 prose-code:text-pink-300">
        <Markdown>{block.text ?? ""}</Markdown>
      </div>
    );
  }
  if (block.type === "tool_use") {
    const inputStr = typeof block.input === "string"
      ? block.input
      : JSON.stringify(block.input, null, 2);
    return (
      <details className="bg-gray-800/60 rounded px-2 py-1">
        <summary className="cursor-pointer text-yellow-300 font-mono text-xs">
          Tool: {block.name}
        </summary>
        <pre className="text-gray-400 text-xs mt-1 overflow-x-auto whitespace-pre-wrap">{inputStr}</pre>
      </details>
    );
  }
  if (block.type === "tool_result") {
    const content = block.content ?? "";
    return (
      <details className="bg-gray-800/60 rounded px-2 py-1">
        <summary className="cursor-pointer text-cyan-300 font-mono text-xs">
          Tool Result
        </summary>
        <pre className="text-gray-400 text-xs mt-1 overflow-x-auto whitespace-pre-wrap">{content.slice(0, 2000)}</pre>
      </details>
    );
  }
  return null;
}

type ViewMode = "terminal" | "logs";

export function SessionDetail() {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [session, setSession] = useState<Session | null>(null);
  const [output, setOutput] = useState("");
  const [message, setMessage] = useState("");
  const [viewMode, setViewMode] = useState<ViewMode>("terminal");
  const [logs, setLogs] = useState<ClaudeLogEntry[]>([]);
  const terminalRef = useRef<HTMLDivElement>(null);
  const logsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!sessionId) return;
    api.getSession(sessionId).then(setSession);
    api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
    api.getSessionLogs(sessionId).then(setLogs).catch(() => {});

    const interval = setInterval(() => {
      api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      api.getSession(sessionId).then(setSession);
      api.getSessionLogs(sessionId).then(setLogs).catch(() => {});
    }, 3000);
    return () => clearInterval(interval);
  }, [sessionId]);

  // Auto-scroll terminal
  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
    }
  }, [output]);

  // Auto-scroll logs
  useEffect(() => {
    if (logsRef.current) {
      logsRef.current.scrollTop = logsRef.current.scrollHeight;
    }
  }, [logs]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || !message.trim()) return;
    await api.sendToSession(sessionId, message);
    setMessage("");
    setTimeout(
      () =>
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output)),
      500
    );
  };

  if (!session) return <div className="p-8">Loading...</div>;

  const sendForm = (
    <form onSubmit={handleSend} className="flex gap-2 mb-6">
      <input
        type="text"
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder="Send a message..."
        className="flex-1 border border-gray-300 rounded px-3 py-2 text-sm"
      />
      <button
        type="submit"
        className="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
      >
        Send
      </button>
    </form>
  );

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <button
        onClick={() => navigate("/")}
        className="text-sm text-gray-500 hover:text-gray-800 mb-4"
      >
        &larr; Back
      </button>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-2xl font-bold">{session.name}</h2>
        <span className="text-sm text-gray-500">{session.status}</span>
        {session.type !== "controller" && (
          <button
            onClick={async () => {
              if (!confirm("Delete this session?")) return;
              await api.deleteSession(session.id);
              navigate("/");
            }}
            className="ml-auto px-3 py-1 text-xs bg-red-50 text-red-600 rounded hover:bg-red-100"
          >
            Delete
          </button>
        )}
      </div>
      <p className="text-sm text-gray-500 mb-4">
        Project: {session.projectPath}
      </p>

      <div className="flex border-b border-gray-300 mb-4">
        <button
          onClick={() => setViewMode("terminal")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px ${
            viewMode === "terminal"
              ? "border-blue-600 text-blue-600"
              : "border-transparent text-gray-500 hover:text-gray-700"
          }`}
        >
          Terminal
        </button>
        <button
          onClick={() => setViewMode("logs")}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px ${
            viewMode === "logs"
              ? "border-blue-600 text-blue-600"
              : "border-transparent text-gray-500 hover:text-gray-700"
          }`}
        >
          Logs
          {logs.length > 0 && (
            <span className="ml-1 text-xs text-gray-400">({logs.length})</span>
          )}
        </button>
      </div>

      {viewMode === "terminal" ? (
        <>
          <div ref={terminalRef} className="bg-gray-900 text-green-400 rounded-lg p-4 mb-4 font-mono text-xs h-96 overflow-y-auto whitespace-pre-wrap">
            {output || "No output yet."}
          </div>
          {sendForm}
        </>
      ) : (
        <>
          <div ref={logsRef} className="bg-gray-900 rounded-lg p-3 text-sm h-96 overflow-y-auto mb-4 space-y-3">
            {logs.length === 0 ? (
              <p className="text-gray-500">No logs yet</p>
            ) : (
              logs.map((log, i) => {
                const style = roleStyles[log.type] || roleStyles.assistant;
                return (
                  <div key={i} className={`rounded-lg p-3 ${style.bg}`}>
                    <div className="flex items-center gap-2 mb-1">
                      <span className={`font-semibold text-xs ${style.text}`}>
                        {style.label}
                      </span>
                      <span className="text-gray-500 text-xs">
                        {new Date(log.timestamp).toLocaleTimeString()}
                      </span>
                    </div>
                    <div className="text-gray-200 break-words text-xs space-y-2">
                      {log.blocks.map((block, j) => (
                        <ContentBlockView key={j} block={block} />
                      ))}
                    </div>
                  </div>
                );
              })
            )}
          </div>
          {sendForm}
        </>
      )}

    </div>
  );
}
