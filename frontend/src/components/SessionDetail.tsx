import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
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
        <Markdown remarkPlugins={[remarkGfm]}>{block.text ?? ""}</Markdown>
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

interface StreamEntry {
  type: string;
  message?: {
    role?: string;
    content?: unknown;
  };
}

function parseStreamBlocks(entry: StreamEntry): ClaudeContentBlock[] {
  const content = entry.message?.content;
  if (!content) return [];

  if (typeof content === "string") {
    return content ? [{ type: "text", text: content }] : [];
  }

  if (!Array.isArray(content)) return [];

  const blocks: ClaudeContentBlock[] = [];
  for (const b of content) {
    if (b.type === "text" && b.text) {
      blocks.push({ type: "text", text: b.text });
    } else if (b.type === "tool_use") {
      blocks.push({ type: "tool_use", name: b.name, input: b.input });
    } else if (b.type === "tool_result") {
      const c = typeof b.content === "string"
        ? b.content
        : JSON.stringify(b.content);
      blocks.push({ type: "tool_result", content: c });
    }
  }
  return blocks;
}

type StreamViewMode = "markdown" | "json";

function isScrolledToBottom(el: HTMLElement, threshold = 50): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
}

function useAutoScroll(dep: unknown) {
  const ref = useRef<HTMLDivElement>(null);
  const wasAtBottom = useRef(true);

  useEffect(() => {
    if (ref.current && wasAtBottom.current) {
      ref.current.scrollTop = ref.current.scrollHeight;
    }
  }, [dep]);

  const onScroll = () => {
    if (ref.current) {
      wasAtBottom.current = isScrolledToBottom(ref.current);
    }
  };

  return { ref, onScroll };
}

function StreamOutputView({ lines }: { lines: unknown[] }) {
  const { ref, onScroll } = useAutoScroll(lines);
  const [viewMode, setViewMode] = useState<StreamViewMode>("markdown");

  const entries = lines
    .map((line) => line as StreamEntry)
    .filter((e) => e.type === "user" || e.type === "assistant");

  return (
    <div>
      <div className="flex justify-end mb-1">
        <button
          onClick={() => setViewMode(viewMode === "markdown" ? "json" : "markdown")}
          className="text-xs text-gray-400 hover:text-gray-200 px-2 py-1"
        >
          {viewMode === "markdown" ? "JSON" : "Markdown"}
        </button>
      </div>
      <div ref={ref} onScroll={onScroll} className="bg-gray-900 rounded-lg p-3 text-sm h-96 overflow-y-auto mb-4 space-y-3">
        {viewMode === "json" ? (
          lines.length === 0 ? (
            <p className="text-gray-500">No stream output yet</p>
          ) : (
            lines.map((line, i) => (
              <pre key={i} className="text-gray-400 text-xs whitespace-pre-wrap">
                {JSON.stringify(line, null, 2)}
              </pre>
            ))
          )
        ) : entries.length === 0 ? (
          <p className="text-gray-500">No stream output yet</p>
        ) : (
          entries.map((entry, i) => {
            const role = entry.type as "user" | "assistant";
            const style = roleStyles[role] || roleStyles.assistant;
            const blocks = parseStreamBlocks(entry);
            if (blocks.length === 0) return null;
            return (
              <div key={i} className={`rounded-lg p-3 ${style.bg}`}>
                <div className="flex items-center gap-2 mb-1">
                  <span className={`font-semibold text-xs ${style.text}`}>
                    {style.label}
                  </span>
                </div>
                <div className="text-gray-200 break-words text-xs space-y-2">
                  {blocks.map((block, j) => (
                    <ContentBlockView key={j} block={block} />
                  ))}
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

export function SessionDetail() {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [session, setSession] = useState<Session | null>(null);
  const [output, setOutput] = useState("");
  const [message, setMessage] = useState("");
  const [logs, setLogs] = useState<ClaudeLogEntry[]>([]);
  const [streamLines, setStreamLines] = useState<unknown[]>([]);
  const terminal = useAutoScroll(output);
  const logsScroll = useAutoScroll(logs);

  useEffect(() => {
    if (!sessionId) return;
    api.getSession(sessionId).then(setSession);
    api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
    api.getSessionLogs(sessionId).then(setLogs).catch(() => {});

    const interval = setInterval(() => {
      api.getSession(sessionId).then(setSession);
      api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      api.getSessionLogs(sessionId).then(setLogs).catch(() => {});
      api.getStreamOutput(sessionId).then(setStreamLines).catch(() => {});
    }, 3000);
    return () => clearInterval(interval);
  }, [sessionId]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || !message.trim()) return;
    await api.sendToSession(sessionId, message);
    setMessage("");
    setTimeout(() => {
      if (session?.outputMode === "stream") {
        api.getStreamOutput(sessionId).then(setStreamLines).catch(() => {});
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    }, 500);
  };

  if (!session) return <div className="p-8">Loading...</div>;

  const isStream = session.outputMode === "stream";

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
      </div>
      <p className="text-sm text-gray-500 mb-4">
        Project: {session.projectPath}
      </p>

      {isStream ? (
        <>
          <StreamOutputView lines={streamLines} />
          {sendForm}
        </>
      ) : (
        <>
          <div ref={terminal.ref} onScroll={terminal.onScroll} className="bg-gray-900 text-green-400 rounded-lg p-4 mb-4 font-mono text-xs h-96 overflow-y-auto whitespace-pre-wrap">
            {output || "No output yet."}
          </div>
          {sendForm}
          <div ref={logsScroll.ref} onScroll={logsScroll.onScroll} className="bg-gray-900 rounded-lg p-3 text-sm h-96 overflow-y-auto mb-4 space-y-3">
            <h3 className="text-gray-400 text-xs font-semibold mb-2">Logs</h3>
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
        </>
      )}

    </div>
  );
}
