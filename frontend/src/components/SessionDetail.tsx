import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Session } from "../types/session";
import { api } from "../api/client";

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-50", label: "User", text: "text-blue-700" },
  assistant: { bg: "bg-gray-50", label: "Assistant", text: "text-green-700" },
};

// --- Stream mode types and helpers ---

interface StreamEntry {
  type: string;
  message?: {
    role?: string;
    content?: unknown;
  };
}

interface StreamContentBlock {
  type: string;
  id?: string;
  tool_use_id?: string;
  text?: string;
  name?: string;
  input?: unknown;
  content?: unknown;
}

// A display item for the merged stream view
type StreamDisplayItem =
  | { kind: "text"; text: string }
  | { kind: "tool_call"; name: string; input: unknown; result?: string };

function parseStreamContentBlocks(entry: StreamEntry): StreamContentBlock[] {
  const content = entry.message?.content;
  if (!content) return [];
  if (typeof content === "string") {
    return content ? [{ type: "text", text: content }] : [];
  }
  if (!Array.isArray(content)) return [];
  return content;
}

// Merge assistant/user entries into display items, pairing tool_use with tool_result by id
function mergeStreamEntries(entries: StreamEntry[]): { role: "user" | "assistant"; items: StreamDisplayItem[] }[] {
  // First pass: collect all tool_results keyed by tool_use_id
  const resultMap = new Map<string, string>();
  for (const entry of entries) {
    if (entry.type !== "user") continue;
    for (const block of parseStreamContentBlocks(entry)) {
      if (block.type === "tool_result" && block.tool_use_id) {
        const c = typeof block.content === "string"
          ? block.content
          : Array.isArray(block.content)
            ? block.content.map((b: { text?: string }) => b.text ?? "").join("")
            : JSON.stringify(block.content);
        resultMap.set(block.tool_use_id, c);
      }
    }
  }

  // Second pass: build display groups from assistant and user text entries
  const groups: { role: "user" | "assistant"; items: StreamDisplayItem[] }[] = [];

  for (const entry of entries) {
    const blocks = parseStreamContentBlocks(entry);

    const role = entry.type as "user" | "assistant";
    const items: StreamDisplayItem[] = [];

    if (entry.type === "assistant") {
      for (const b of blocks) {
        if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        } else if (b.type === "tool_use") {
          items.push({
            kind: "tool_call",
            name: b.name ?? "unknown",
            input: b.input,
            result: b.id ? resultMap.get(b.id) : undefined,
          });
        }
      }
    } else if (entry.type === "user") {
      // Only show user text content (tool_results are merged into assistant tool_call items)
      for (const b of blocks) {
        if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        }
      }
      // user entries with content as plain string
      if (items.length === 0 && typeof entry.message?.content === "string" && entry.message.content) {
        items.push({ kind: "text", text: entry.message.content });
      }
    }

    if (items.length > 0) {
      // Merge into previous group if same role
      const last = groups[groups.length - 1];
      if (last && last.role === role) {
        last.items.push(...items);
      } else {
        groups.push({ role, items });
      }
    }
  }

  return groups;
}

function extractFileName(filePath: string): string {
  const parts = filePath.split("/");
  return parts[parts.length - 1] || filePath;
}

function toolCallSummary(name: string, input: unknown): string {
  const inp = input && typeof input === "object" ? (input as Record<string, unknown>) : null;
  if (name === "Bash" && inp && "command" in inp) {
    if ("description" in inp && inp.description) {
      return `Bash: ${String(inp.description)}`;
    }
    const cmd = String(inp.command);
    const firstLine = cmd.split("\n")[0];
    return `Bash(${firstLine})`;
  }
  if ((name === "Read" || name === "Write" || name === "Edit") && inp && "file_path" in inp) {
    const fileName = extractFileName(String(inp.file_path));
    return `${name}(${fileName})`;
  }
  return `Tool: ${name}`;
}

function ToolCallView({ item }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }> }) {
  const inputStr = typeof item.input === "string"
    ? item.input
    : JSON.stringify(item.input, null, 2);
  return (
    <details className="bg-gray-100 rounded px-2 py-1">
      <summary className="cursor-pointer text-yellow-700 font-mono text-xs">
        {toolCallSummary(item.name, item.input)}{item.result !== undefined ? " ✔" : ""}
      </summary>
      <div className="mt-1 space-y-1">
        <div>
          <span className="text-gray-500 text-xs">Input:</span>
          <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap">{inputStr}</pre>
        </div>
        {item.result !== undefined && (
          <div>
            <span className="text-gray-500 text-xs">Output:</span>
            <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap">{item.result.slice(0, 2000)}</pre>
          </div>
        )}
      </div>
    </details>
  );
}

function StreamDisplayItemView({ item }: { item: StreamDisplayItem }) {
  if (item.kind === "text") {
    return (
      <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600">
        <Markdown remarkPlugins={[remarkGfm]}>{item.text}</Markdown>
      </div>
    );
  }
  if (item.kind === "tool_call") {
    return <ToolCallView item={item} />;
  }
  return null;
}

// --- Shared components ---

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

  const groups = mergeStreamEntries(entries);

  return (
    <div>
      <div className="flex justify-end mb-1">
        <button
          onClick={() => setViewMode(viewMode === "markdown" ? "json" : "markdown")}
          className="text-xs text-gray-500 hover:text-gray-700 px-2 py-1"
        >
          {viewMode === "markdown" ? "JSON" : "Markdown"}
        </button>
      </div>
      <div ref={ref} onScroll={onScroll} className="bg-white border border-gray-200 rounded-lg p-3 text-sm h-96 overflow-y-auto mb-4 space-y-3">
        {viewMode === "json" ? (
          lines.length === 0 ? (
            <p className="text-gray-400">No stream output yet</p>
          ) : (
            lines.map((line, i) => (
              <pre key={i} className="text-gray-600 text-xs whitespace-pre-wrap">
                {JSON.stringify(line, null, 2)}
              </pre>
            ))
          )
        ) : groups.length === 0 ? (
          <p className="text-gray-400">No stream output yet</p>
        ) : (
          groups.map((group, i) => {
            const style = roleStyles[group.role] || roleStyles.assistant;
            return (
              <div key={i} className={`rounded-lg p-3 ${style.bg}`}>
                <div className="flex items-center gap-2 mb-1">
                  <span className={`font-semibold text-xs ${style.text}`}>
                    {style.label}
                  </span>
                </div>
                <div className="text-gray-800 break-words text-xs space-y-2">
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} />
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
  const [streamLines, setStreamLines] = useState<unknown[]>([]);
  const terminal = useAutoScroll(output);

  useEffect(() => {
    if (!sessionId) return;
    api.getSession(sessionId).then(setSession);
    api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
    api.getStreamOutput(sessionId).then(setStreamLines).catch(() => {});

    const interval = setInterval(() => {
      api.getSession(sessionId).then(setSession);
      api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
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
        {session.type !== "controller" && (
          <div className="ml-auto flex gap-2">
            {session.status !== "stopped" && (
              <button
                onClick={async () => {
                  await api.stopSession(session.id);
                  api.getSession(session.id).then(setSession);
                }}
                className="px-3 py-1 text-xs bg-yellow-50 text-yellow-700 rounded hover:bg-yellow-100"
              >
                Stop
              </button>
            )}
            <button
              onClick={async () => {
                if (!confirm("Delete this session?")) return;
                await api.deleteSession(session.id);
                navigate("/");
              }}
              className="px-3 py-1 text-xs bg-red-50 text-red-600 rounded hover:bg-red-100"
            >
              Delete
            </button>
          </div>
        )}
      </div>
      <p className="text-sm text-gray-500 mb-2">
        Project: {session.projectPath}
        {session.githubUrl && (
          <>
            {" "}
            <a
              href={session.githubUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-gray-600 hover:text-gray-900"
              title="Open on GitHub"
            >
              <svg className="w-4 h-4" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
            </a>
          </>
        )}
      </p>
      {session.branch && (
        <p className="text-sm text-gray-500 mb-2">
          Branch: <span className="font-mono text-gray-700">{session.branch}</span>
        </p>
      )}
      {session.pullRequests && session.pullRequests.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-4">
          {session.pullRequests.map((pr) => (
            <a
              key={pr.number}
              href={pr.url}
              target="_blank"
              rel="noopener noreferrer"
              className={`inline-flex items-center gap-1 text-xs px-2 py-1 rounded-full ${
                pr.state === "MERGED"
                  ? "bg-purple-100 text-purple-700"
                  : pr.state === "OPEN"
                    ? "bg-green-100 text-green-700"
                    : "bg-gray-100 text-gray-600"
              }`}
            >
              #{pr.number} {pr.title}
            </a>
          ))}
        </div>
      )}

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
        </>
      )}

    </div>
  );
}
