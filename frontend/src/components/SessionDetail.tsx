import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Square, RefreshCw, Trash2, ArrowLeft, GitBranch, GitPullRequest, FileDiff,
  Terminal, FileText, FilePen, PenLine, Search, Sparkles, Globe, Wrench, CheckCircle2, ChevronRight,
} from "lucide-react";
import type { Session } from "../types/session";
import { api, type DiffFile } from "../api/client";

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
  | { kind: "tool_call"; name: string; input: unknown; result?: string }
  | { kind: "system_event"; eventType: string; label: string; detail?: string };

function parseStreamContentBlocks(entry: StreamEntry): StreamContentBlock[] {
  const content = entry.message?.content;
  if (!content) return [];
  if (typeof content === "string") {
    return content ? [{ type: "text", text: content }] : [];
  }
  if (!Array.isArray(content)) return [];
  return content;
}

// Parse a system event entry into a display item, or return null if not displayable
function parseSystemEvent(entry: StreamEntry): StreamDisplayItem | null {
  const raw = entry as unknown as Record<string, unknown>;
  const subtype = raw.subtype as string | undefined;

  if (subtype === "compact_boundary") {
    const meta = raw.compact_metadata as { trigger?: string; pre_tokens?: number } | undefined;
    const tokens = meta?.pre_tokens ? `${Math.round(meta.pre_tokens / 1000)}k tokens` : "";
    return {
      kind: "system_event",
      eventType: "compact",
      label: "コンテキストをコンパクション",
      detail: tokens ? `(${tokens})` : undefined,
    };
  }

  if (subtype === "status") {
    const status = raw.status as string | null;
    if (status === "compacting") {
      return {
        kind: "system_event",
        eventType: "compacting",
        label: "コンパクション中...",
      };
    }
    // status: null means compaction finished — skip (compact_boundary covers it)
    return null;
  }

  if (subtype === "task_started") {
    const desc = (raw.description as string) || "バックグラウンドタスク";
    return {
      kind: "system_event",
      eventType: "task_started",
      label: `タスク開始: ${desc}`,
    };
  }

  if (subtype === "task_notification") {
    const status = raw.status as string;
    const summary = (raw.summary as string) || "";
    return {
      kind: "system_event",
      eventType: "task_notification",
      label: `タスク${status === "completed" ? "完了" : status}`,
      detail: summary || undefined,
    };
  }

  return null;
}

type DisplayGroup =
  | { role: "user" | "assistant"; items: StreamDisplayItem[] }
  | { role: "system"; items: StreamDisplayItem[] };

// Merge assistant/user entries into display items, pairing tool_use with tool_result by id
function mergeStreamEntries(entries: StreamEntry[]): DisplayGroup[] {
  // First pass: collect all tool_results keyed by tool_use_id, and track Skill tool IDs
  const resultMap = new Map<string, string>();
  const skillToolIds = new Set<string>();
  for (const entry of entries) {
    if (entry.type === "assistant") {
      for (const block of parseStreamContentBlocks(entry)) {
        if (block.type === "tool_use" && block.name === "Skill" && block.id) {
          skillToolIds.add(block.id);
        }
      }
    }
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

  // Second pass: build display groups
  const groups: DisplayGroup[] = [];
  const skillSkipIndices = new Set<number>();

  for (let idx = 0; idx < entries.length; idx++) {
    const entry = entries[idx];

    // Handle system events
    if (entry.type === "system") {
      const sysItem = parseSystemEvent(entry);
      if (sysItem) {
        groups.push({ role: "system", items: [sysItem] });
      }
      continue;
    }

    const blocks = parseStreamContentBlocks(entry);

    const role = entry.type as "user" | "assistant";
    const items: StreamDisplayItem[] = [];

    if (entry.type === "assistant") {
      for (const b of blocks) {
        if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        } else if (b.type === "tool_use") {
          // Detect plan mode transitions
          if (b.name === "EnterPlanMode") {
            groups.push({ role: "system", items: [{ kind: "system_event", eventType: "plan_enter", label: "プランモードに入りました" }] });
            continue;
          }
          if (b.name === "ExitPlanMode") {
            groups.push({ role: "system", items: [{ kind: "system_event", eventType: "plan_exit", label: "プランモードを終了しました" }] });
            continue;
          }

          const result = b.id ? resultMap.get(b.id) : undefined;

          // For Skill calls, fold the next user text (skill content) into the result
          if (b.name === "Skill" && b.id && skillToolIds.has(b.id)) {
            // Pattern: tool_use(Skill) → user(tool_result) → user(text = skill content)
            // Scan forward past tool_result entries to find the text-only user entry
            let skillContent = result || "";
            for (let j = idx + 1; j < entries.length && j <= idx + 4; j++) {
              const nextEntry = entries[j];
              if (nextEntry.type !== "user") break;
              const nextBlocks = parseStreamContentBlocks(nextEntry);
              const textBlocks = nextBlocks.filter(nb => nb.type === "text" && nb.text);
              const hasToolResult = nextBlocks.some(nb => nb.type === "tool_result");
              if (hasToolResult) continue; // skip the tool_result entry
              if (textBlocks.length > 0) {
                skillContent += "\n---\n" + textBlocks.map(tb => tb.text).join("\n");
                skillSkipIndices.add(j);
                break;
              }
            }
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result: skillContent || undefined,
            });
          } else {
            items.push({
              kind: "tool_call",
              name: b.name ?? "unknown",
              input: b.input,
              result,
            });
          }
        }
      }
    } else if (entry.type === "user") {
      // Skip user text that was folded into a Skill tool call
      if (skillSkipIndices.has(idx)) {
        continue;
      }

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


function toolIcon(name: string) {
  switch (name) {
    case "Bash": return Terminal;
    case "Read": return FileText;
    case "Write": return FilePen;
    case "Edit": return PenLine;
    case "Grep":
    case "Glob":
    case "ToolSearch": return Search;
    case "Skill": return Sparkles;
    case "WebFetch":
    case "WebSearch": return Globe;
    default: return Wrench;
  }
}

function toolDescription(name: string, input: unknown): string | null {
  const inp = input && typeof input === "object" ? (input as Record<string, unknown>) : null;
  if (name === "Bash" && inp) {
    if ("description" in inp && inp.description) return String(inp.description);
    if ("command" in inp) return String(inp.command).split("\n")[0].slice(0, 120);
  }
  if ((name === "Read" || name === "Write" || name === "Edit") && inp && "file_path" in inp) {
    const fp = String(inp.file_path);
    const parts = fp.split("/");
    return parts[parts.length - 1] || fp;
  }
  if (name === "Skill" && inp && "skill" in inp) {
    const args = inp.args ? ` ${String(inp.args).split("\n")[0]}` : "";
    return `${String(inp.skill)}${args}`;
  }
  if ((name === "Grep" || name === "Glob") && inp && "pattern" in inp) {
    return String(inp.pattern);
  }
  if (name === "ToolSearch" && inp && "query" in inp) {
    return String(inp.query);
  }
  return null;
}

function toolSubDetail(name: string, input: unknown): string | null {
  const inp = input && typeof input === "object" ? (input as Record<string, unknown>) : null;
  if (name === "Bash" && inp && "command" in inp) {
    const cmd = String(inp.command).split("\n")[0].slice(0, 120);
    // descriptionがある場合のみコマンドをサブ詳細として表示
    if ("description" in inp && inp.description) return `$ ${cmd}`;
  }
  return null;
}

function ToolCallView({ item }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }> }) {
  const inputStr = typeof item.input === "string"
    ? item.input
    : JSON.stringify(item.input, null, 2);
  const Icon = toolIcon(item.name);
  const desc = toolDescription(item.name, item.input);
  const subDetail = toolSubDetail(item.name, item.input);
  const done = item.result !== undefined;

  return (
    <details className="group border border-gray-200 rounded-lg overflow-hidden">
      <summary className="cursor-pointer px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors list-none [&::-webkit-details-marker]:hidden">
        <div className="flex items-center gap-2">
          <ChevronRight className="w-3 h-3 text-gray-400 shrink-0 transition-transform group-open:rotate-90" />
          <Icon className="w-3.5 h-3.5 text-gray-500 shrink-0" />
          <span className="font-medium text-xs text-gray-800">{item.name}</span>
          {desc && (
            <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>
          )}
          {done && <CheckCircle2 className="w-3 h-3 text-green-500 ml-auto shrink-0" />}
        </div>
        {subDetail && (
          <div className="mt-0.5 ml-[22px] font-mono text-[11px] text-gray-400 truncate">{subDetail}</div>
        )}
      </summary>
      <div className="px-2.5 py-2 space-y-1 border-t border-gray-100 bg-white">
        <div>
          <span className="text-gray-400 text-[10px] uppercase tracking-wide">Input</span>
          <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{inputStr}</pre>
        </div>
        {done && (
          <div>
            <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
            <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{item.result!.slice(0, 2000)}</pre>
          </div>
        )}
      </div>
    </details>
  );
}

function SystemEventView({ item }: { item: Extract<StreamDisplayItem, { kind: "system_event" }> }) {
  return (
    <div className="flex items-center gap-2 py-1 px-3 text-xs text-gray-500 border-y border-dashed border-gray-200">
      <span className="text-gray-400">⟡</span>
      <span>{item.label}</span>
      {item.detail && <span className="text-gray-400">{item.detail}</span>}
    </div>
  );
}

const COLLAPSE_LINE_THRESHOLD = 20;

function CollapsibleText({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const lineCount = text.split("\n").length;
  const shouldCollapse = lineCount > COLLAPSE_LINE_THRESHOLD;

  if (!shouldCollapse || expanded) {
    return (
      <div>
        <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600">
          <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
        </div>
        {shouldCollapse && (
          <button onClick={() => setExpanded(false)} className="text-xs text-blue-500 hover:text-blue-700 mt-1">
            折りたたむ
          </button>
        )}
      </div>
    );
  }

  const preview = text.split("\n").slice(0, 5).join("\n");
  return (
    <div>
      <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600 relative overflow-hidden max-h-24">
        <Markdown remarkPlugins={[remarkGfm]}>{preview}</Markdown>
      </div>
      <button onClick={() => setExpanded(true)} className="text-xs text-blue-500 hover:text-blue-700 mt-1">
        続きを表示 ({lineCount} 行)
      </button>
    </div>
  );
}

function StreamDisplayItemView({ item }: { item: StreamDisplayItem }) {
  if (item.kind === "text") {
    return <CollapsibleText text={item.text} />;
  }
  if (item.kind === "tool_call") {
    return <ToolCallView item={item} />;
  }
  if (item.kind === "system_event") {
    return <SystemEventView item={item} />;
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

function StreamOutputView({ lines, className }: { lines: unknown[]; className?: string }) {
  const { ref, onScroll } = useAutoScroll(lines);
  const [viewMode, setViewMode] = useState<StreamViewMode>("markdown");

  const entries = lines
    .map((line) => line as StreamEntry)
    .filter((e) => e.type === "user" || e.type === "assistant" || e.type === "system");

  const groups = mergeStreamEntries(entries);

  return (
    <div className={`flex flex-col ${className || ""}`}>
      <div className="flex justify-end mb-1 shrink-0">
        <button
          onClick={() => setViewMode(viewMode === "markdown" ? "json" : "markdown")}
          className="text-xs text-gray-500 hover:text-gray-700 px-2 py-1"
        >
          {viewMode === "markdown" ? "JSON" : "Markdown"}
        </button>
      </div>
      <div ref={ref} onScroll={onScroll} className="bg-white border border-gray-200 rounded-lg p-3 text-sm flex-1 min-h-0 overflow-y-auto space-y-3">
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
            if (group.role === "system") {
              return (
                <div key={i}>
                  {group.items.map((item, j) => (
                    <StreamDisplayItemView key={j} item={item} />
                  ))}
                </div>
              );
            }
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

const statusBadgeColor: Record<string, string> = {
  M: "bg-yellow-100 text-yellow-800",
  A: "bg-green-100 text-green-800",
  D: "bg-red-100 text-red-800",
  R: "bg-blue-100 text-blue-800",
  "?": "bg-gray-100 text-gray-600",
};

function DiffDropdown({ files }: { files: DiffFile[] }) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  if (files.length === 0) return null;

  const toggle = (path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  return (
    <>
      <button
        onClick={() => setOpen(!open)}
        className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-orange-50 text-orange-700 hover:bg-orange-100"
        title="Changes"
      >
        <FileDiff className="w-3 h-3" />
        {files.length}
      </button>
      {open && (
        <div className="basis-full border border-gray-200 rounded-lg overflow-hidden mt-1">
          {files.map((file) => (
            <div key={file.path} className="border-b border-gray-100 last:border-b-0">
              <button
                onClick={() => toggle(file.path)}
                className="w-full flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-gray-50 text-left"
              >
                <span className={`px-1.5 py-0.5 rounded font-mono text-[10px] font-bold ${statusBadgeColor[file.status] || "bg-gray-100 text-gray-600"}`}>
                  {file.status}
                </span>
                <span className="font-mono text-gray-700 truncate">{file.path}</span>
                {file.diff && (
                  <span className="ml-auto text-gray-400">{expanded.has(file.path) ? "▼" : "▶"}</span>
                )}
              </button>
              {expanded.has(file.path) && file.diff && (
                <pre className="bg-gray-900 text-gray-200 text-xs p-3 overflow-x-auto whitespace-pre font-mono">
                  {file.diff.split("\n").map((line, i) => (
                    <span
                      key={i}
                      className={
                        line.startsWith("+") && !line.startsWith("+++")
                          ? "text-green-400"
                          : line.startsWith("-") && !line.startsWith("---")
                            ? "text-red-400"
                            : line.startsWith("@@")
                              ? "text-blue-400"
                              : ""
                      }
                    >
                      {line}
                      {"\n"}
                    </span>
                  ))}
                </pre>
              )}
            </div>
          ))}
        </div>
      )}
    </>
  );
}

export function SessionDetail() {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [session, setSession] = useState<Session | null>(null);
  const [output, setOutput] = useState("");
  const [message, setMessage] = useState("");
  const [streamLines, setStreamLines] = useState<unknown[]>([]);
  const [diffFiles, setDiffFiles] = useState<DiffFile[]>([]);
  const terminal = useAutoScroll(output);

  useEffect(() => {
    if (!sessionId) return;
    api.getSession(sessionId).then((s) => {
      setSession(s);
      if (s.outputMode === "stream") {
        api.getStreamOutput(sessionId).then(setStreamLines).catch(() => {});
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    });
    api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});

    const interval = setInterval(() => {
      api.getSession(sessionId).then((s) => {
        setSession(s);
        if (s.outputMode === "stream") {
          api.getStreamOutput(sessionId).then(setStreamLines).catch(() => {});
        } else {
          api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
        }
      });
      api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
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
    <form onSubmit={handleSend} className="flex gap-2 shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8 border-t border-gray-100">
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
    <div className="h-dvh flex flex-col px-4 sm:px-8 pt-4 sm:pt-8 max-w-4xl mx-auto">
      <div className="flex flex-wrap items-center gap-2 sm:gap-3 mb-3 shrink-0">
        <button
          onClick={() => navigate("/")}
          className="p-1 text-gray-400 hover:text-gray-700 rounded hover:bg-gray-100 shrink-0"
          title="Back"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        <h2 className="text-xl sm:text-2xl font-bold">{session.name}</h2>
        <span className="text-xs sm:text-sm text-gray-500">{session.status}</span>
        {session.type !== "controller" && (
          <div className="flex gap-1.5 sm:ml-auto">
            {session.status !== "stopped" && (
              <button
                onClick={async () => {
                  await api.stopSession(session.id);
                  api.getSession(session.id).then(setSession);
                }}
                className="p-1.5 text-yellow-700 bg-yellow-50 rounded hover:bg-yellow-100"
                title="Stop"
              >
                <Square className="w-3.5 h-3.5" />
              </button>
            )}
            <button
              onClick={async () => {
                await api.reconnectSession(session.id);
                api.getSession(session.id).then(setSession);
              }}
              className="p-1.5 text-indigo-700 bg-indigo-50 rounded hover:bg-indigo-100"
              title="Reconnect"
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={async () => {
                if (!confirm("Delete this session?")) return;
                await api.deleteSession(session.id);
                navigate("/");
              }}
              className="p-1.5 text-red-600 bg-red-50 rounded hover:bg-red-100"
              title="Delete"
            >
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          </div>
        )}
      </div>
      <p className="text-xs sm:text-sm text-gray-500 mb-2 shrink-0 truncate" title={session.projectPath}>
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
      {(session.branch || (session.pullRequests && session.pullRequests.length > 0) || diffFiles.length > 0) && (
        <div className="flex flex-wrap items-center gap-2 mb-2 shrink-0">
          {session.branch && (
            <>
              <GitBranch className="w-3.5 h-3.5 text-gray-400 shrink-0" />
              <span className="font-mono text-xs text-gray-700">{session.branch}</span>
            </>
          )}
          {session.pullRequests && session.pullRequests.map((pr) => (
            <a
              key={pr.number}
              href={pr.url}
              target="_blank"
              rel="noopener noreferrer"
              className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full ${
                pr.state === "MERGED"
                  ? "bg-purple-100 text-purple-700"
                  : pr.state === "OPEN"
                    ? "bg-green-100 text-green-700"
                    : "bg-gray-100 text-gray-600"
              }`}
            >
              <GitPullRequest className="w-3 h-3" />
              #{pr.number}
            </a>
          ))}
          <DiffDropdown files={diffFiles} />
        </div>
      )}

      {(session.currentTask || session.goal) && (
        <div className="bg-indigo-50 border border-indigo-200 rounded-lg p-3 mb-4">
          {session.goals && session.goals.length > 1 && (
            <div className="text-xs text-indigo-400 mb-2">
              {session.goals.slice(0, -1).map((g, i) => (
                <span key={i}>
                  {i > 0 && " > "}
                  {g.goal}
                </span>
              ))}
              {" > "}
            </div>
          )}
          {session.currentTask && (
            <p className="text-sm text-indigo-800">
              <span className="font-semibold">Task:</span> {session.currentTask}
            </p>
          )}
          {session.goal && (
            <p className="text-sm text-indigo-600 mt-1">
              <span className="font-semibold">Goal:</span> {session.goal}
            </p>
          )}
        </div>
      )}

      {isStream ? (
        <div className="flex flex-col flex-1 min-h-0">
          <StreamOutputView lines={streamLines} className="flex-1 min-h-0" />
          {sendForm}
        </div>
      ) : (
        <div className="flex flex-col flex-1 min-h-0">
          <div ref={terminal.ref} onScroll={terminal.onScroll} className="bg-gray-900 text-green-400 rounded-lg p-4 font-mono text-xs flex-1 min-h-0 overflow-y-auto whitespace-pre-wrap">
            {output || "No output yet."}
          </div>
          {sendForm}
        </div>
      )}

    </div>
  );
}
