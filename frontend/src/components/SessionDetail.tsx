import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Square, RefreshCw, Trash2, ArrowLeft, GitBranch, GitPullRequest, FileDiff, X, FolderOpen,
  Terminal, FileText, FilePen, PenLine, Search, Sparkles, Globe, Wrench, CheckCircle2,
  ListTodo, Target, RotateCcw, Circle, Bot, ImagePlus, SendHorizonal, AlertTriangle, Plus, Slash,
} from "lucide-react";
import type { Session } from "../types/session";
import { api, type DiffFile } from "../api/client";
import { StatusDot } from "./StatusBadge";
import { setActiveSessionName } from "../activeSession";
import { useWebSocket } from "../hooks/useWebSocket";

const roleStyles: Record<string, { bg: string; label: string; text: string }> = {
  user: { bg: "bg-blue-50", label: "User", text: "text-blue-700" },
  assistant: { bg: "bg-gray-50", label: "Assistant", text: "text-green-700" },
};

// --- Stream mode types and helpers ---

interface StreamEntry {
  type: string;
  parent_tool_use_id?: string | null;
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
  source?: { type: string; media_type: string; data: string };
}

// AskUserQuestion input types
interface AskUserQuestionOption {
  label: string;
  description?: string;
}

interface AskUserQuestionItem {
  question: string;
  header?: string;
  options: AskUserQuestionOption[];
  multiSelect?: boolean;
}

// A display item for the merged stream view
type StreamDisplayItem =
  | { kind: "text"; text: string }
  | { kind: "image"; mediaType: string; data: string }
  | { kind: "tool_call"; name: string; input: unknown; result?: string; resultImages?: Array<{ mediaType: string; data: string }>; toolUseId?: string; children?: StreamDisplayItem[] }
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
    return null;
  }

  if (subtype === "init") {
    return null;
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
  const resultImageMap = new Map<string, Array<{ mediaType: string; data: string }>>();
  const skillToolIds = new Set<string>();
  const toolSearchToolIds = new Set<string>();
  const askUserToolIds = new Set<string>();
  const agentToolIds = new Set<string>();
  // Collect child entries grouped by parent_tool_use_id
  const childEntriesMap = new Map<string, StreamEntry[]>();
  for (const entry of entries) {
    // Track entries with parent_tool_use_id
    if (entry.parent_tool_use_id) {
      const children = childEntriesMap.get(entry.parent_tool_use_id) || [];
      children.push(entry);
      childEntriesMap.set(entry.parent_tool_use_id, children);
    }
    if (entry.type === "assistant") {
      for (const block of parseStreamContentBlocks(entry)) {
        if (block.type === "tool_use" && block.name === "Skill" && block.id) {
          skillToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "ToolSearch" && block.id) {
          toolSearchToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "AskUserQuestion" && block.id) {
          askUserToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "Agent" && block.id) {
          agentToolIds.add(block.id);
        }
      }
    }
    if (entry.type !== "user") continue;
    for (const block of parseStreamContentBlocks(entry)) {
      // Skip AskUserQuestion tool_results (auto-answered by CLI)
      if (block.type === "tool_result" && block.tool_use_id && askUserToolIds.has(block.tool_use_id)) {
        continue;
      }
      if (block.type === "tool_result" && block.tool_use_id) {
        if (Array.isArray(block.content)) {
          const texts: string[] = [];
          const images: Array<{ mediaType: string; data: string }> = [];
          for (const b of block.content as Array<{ type?: string; text?: string; source?: { media_type: string; data: string } }>) {
            if (b.type === "image" && b.source) {
              images.push({ mediaType: b.source.media_type, data: b.source.data });
            } else if (b.text) {
              texts.push(b.text);
            }
          }
          resultMap.set(block.tool_use_id, texts.join(""));
          if (images.length > 0) {
            resultImageMap.set(block.tool_use_id, images);
          }
        } else {
          const c = typeof block.content === "string"
            ? block.content
            : JSON.stringify(block.content);
          resultMap.set(block.tool_use_id, c);
        }
      }
    }
  }

  // Helper: build child tool_call items from child entries of an Agent
  function buildChildItems(parentToolUseId: string): StreamDisplayItem[] {
    const childEntries = childEntriesMap.get(parentToolUseId) || [];
    const items: StreamDisplayItem[] = [];
    for (const entry of childEntries) {
      if (entry.type !== "assistant") continue;
      for (const b of parseStreamContentBlocks(entry)) {
        if (b.type === "tool_use" && b.name) {
          const result = b.id ? resultMap.get(b.id) : undefined;
          const resultImages = b.id ? resultImageMap.get(b.id) : undefined;
          // Recursively build children if this is also an Agent
          const children = (b.name === "Agent" && b.id && childEntriesMap.has(b.id))
            ? buildChildItems(b.id)
            : undefined;
          items.push({
            kind: "tool_call",
            name: b.name,
            input: b.input,
            result,
            resultImages,
            toolUseId: b.id,
            children: children && children.length > 0 ? children : undefined,
          });
        } else if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        }
      }
    }
    return items;
  }

  // Second pass: build display groups
  const groups: DisplayGroup[] = [];
  const skillSkipIndices = new Set<number>();
  for (let idx = 0; idx < entries.length; idx++) {
    const entry = entries[idx];

    // Skip entries that belong to a parent Agent that is present in the current data
    if (entry.parent_tool_use_id && agentToolIds.has(entry.parent_tool_use_id)) {
      continue;
    }

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
          const resultImages = b.id ? resultImageMap.get(b.id) : undefined;

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
              resultImages,
            });
          } else if (b.name === "ToolSearch" && b.id && toolSearchToolIds.has(b.id)) {
            // Skip the "Tool loaded." user text after ToolSearch
            for (let j = idx + 1; j < entries.length && j <= idx + 4; j++) {
              const nextEntry = entries[j];
              if (nextEntry.type !== "user") break;
              const nextBlocks = parseStreamContentBlocks(nextEntry);
              const hasToolResult = nextBlocks.some(nb => nb.type === "tool_result");
              if (hasToolResult) continue;
              const textBlocks = nextBlocks.filter(nb => nb.type === "text" && nb.text);
              if (textBlocks.length > 0) {
                skillSkipIndices.add(j);
                break;
              }
            }
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result,
              resultImages,
            });
          } else if (b.name === "Agent" && b.id && agentToolIds.has(b.id)) {
            // Skip the subagent prompt user text after Agent tool call
            // System entries (task_started, task_progress) may appear between the tool call and the user prompt
            for (let j = idx + 1; j < entries.length && j <= idx + 8; j++) {
              const nextEntry = entries[j];
              if (nextEntry.type === "system") continue; // skip system entries
              if (nextEntry.type !== "user") break;
              const nextBlocks = parseStreamContentBlocks(nextEntry);
              const hasToolResult = nextBlocks.some(nb => nb.type === "tool_result");
              if (hasToolResult) continue;
              const textBlocks = nextBlocks.filter(nb => nb.type === "text" && nb.text);
              if (textBlocks.length > 0) {
                skillSkipIndices.add(j);
                break;
              }
            }
            // Build child items from entries with parent_tool_use_id matching this Agent
            const children = buildChildItems(b.id);
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result,
              resultImages,
              toolUseId: b.id,
              children: children.length > 0 ? children : undefined,
            });
          } else {
            items.push({
              kind: "tool_call",
              name: b.name ?? "unknown",
              input: b.input,
              result,
              resultImages,
            });
          }
        }
      }
    } else if (entry.type === "user") {
      // Skip user text that was folded into a Skill tool call
      if (skillSkipIndices.has(idx)) {
        continue;
      }

      // Only show user text/image content (tool_results are merged into assistant tool_call items)
      for (const b of blocks) {
        if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        } else if (b.type === "image" && b.source) {
          items.push({ kind: "image", mediaType: b.source.media_type, data: b.source.data });
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
    case "Agent": return Bot;
    case "mcp__agmux__escalate": return AlertTriangle;
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
  if (name === "WebSearch" && inp && "query" in inp) {
    return String(inp.query);
  }
  if (name === "Agent" && inp && "description" in inp) {
    return String(inp.description);
  }
  if (name === "AskUserQuestion" && inp && "questions" in inp) {
    const questions = inp.questions as AskUserQuestionItem[];
    return questions[0]?.question?.slice(0, 60) || null;
  }
  if (name === "mcp__agmux__escalate" && inp && "message" in inp) {
    return String(inp.message).slice(0, 80);
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

function Modal({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: React.ReactNode; children: React.ReactNode }) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center" onClick={onClose}>
      <div className="fixed inset-0 bg-black/30" />
      <div
        className="relative bg-white rounded-t-xl sm:rounded-xl shadow-xl w-full sm:max-w-2xl max-h-[80vh] flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-100 shrink-0">
          <div className="text-sm font-medium text-gray-800 min-w-0 truncate">{title}</div>
          <button onClick={onClose} className="p-1 text-gray-400 hover:text-gray-700 rounded hover:bg-gray-100 shrink-0">
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="overflow-y-auto p-4">{children}</div>
      </div>
    </div>
  );
}

function ToolInputView({ input }: { input: unknown }) {
  if (input && typeof input === "object" && !Array.isArray(input)) {
    const entries = Object.entries(input as Record<string, unknown>);
    return (
      <div className="space-y-2">
        {entries.map(([key, value]) => {
          const str = typeof value === "string" ? value : JSON.stringify(value, null, 2);
          const isMultiline = str.includes("\n");
          return (
            <div key={key}>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">{key}</span>
              {isMultiline ? (
                <pre className="text-gray-700 text-xs mt-0.5 bg-gray-50 border border-gray-200 rounded p-2 overflow-x-auto whitespace-pre-wrap">{str}</pre>
              ) : (
                <div className="text-gray-700 text-xs mt-0.5 font-mono">{str}</div>
              )}
            </div>
          );
        })}
      </div>
    );
  }
  const str = typeof input === "string" ? input : JSON.stringify(input, null, 2);
  return <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap">{str}</pre>;
}

function AskUserQuestionCallView({ item, onAnswer }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }>; onAnswer?: (text: string) => void }) {
  const [expanded, setExpanded] = useState(true);
  const inp = item.input as { questions?: AskUserQuestionItem[] } | undefined;
  const Icon = toolIcon(item.name);
  const desc = toolDescription(item.name, item.input);

  return (
    <div className="border border-orange-200 rounded-lg overflow-hidden bg-orange-50">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-2.5 py-1.5 hover:bg-orange-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <Icon className="w-3.5 h-3.5 text-orange-500 shrink-0" />
          <span className="font-medium text-xs text-orange-800">{item.name}</span>
          {desc && (
            <span className="text-xs text-orange-600 truncate min-w-0">{desc}</span>
          )}
        </div>
      </button>
      {expanded && inp?.questions && (
        <div className="px-3 pb-3 space-y-3">
          {inp.questions.map((q, qi) => (
            <div key={qi}>
              {q.header && (
                <span className="inline-block px-1.5 py-0.5 text-[10px] font-medium bg-orange-200 text-orange-800 rounded mb-1">
                  {q.header}
                </span>
              )}
              <p className="text-sm text-gray-800 mb-2">{q.question}</p>
              <div className="flex flex-wrap gap-2">
                {q.options.map((opt, oi) => (
                  <button
                    key={oi}
                    onClick={() => onAnswer?.(opt.label)}
                    className="px-3 py-1.5 text-xs rounded-lg border bg-white border-gray-300 text-gray-700 hover:bg-blue-50 hover:border-blue-300 hover:text-blue-700 transition-colors"
                    title={opt.description}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function formatTimeout(seconds: number): string {
  if (seconds >= 60) {
    const min = Math.floor(seconds / 60);
    return `${min}分`;
  }
  return `${seconds}秒`;
}

function EscalateCallView({ item, sessionId, escalationId, timedOut, timeoutSeconds, onResponded }: {
  item: Extract<StreamDisplayItem, { kind: "tool_call" }>;
  sessionId?: string;
  escalationId?: string;
  timedOut?: boolean;
  timeoutSeconds?: number;
  onResponded?: () => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const [response, setResponse] = useState("");
  const [sending, setSending] = useState(false);
  const inp = item.input as { message?: string } | undefined;
  const isResolved = item.result !== undefined;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || !response.trim() || sending) return;

    if (!escalationId) return;
    const escId = escalationId;
    setSending(true);
    try {
      await api.respondEscalation(sessionId, escId, response.trim());
      setResponse("");
      onResponded?.();
    } catch {
      // ignore errors
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="border border-red-200 rounded-lg overflow-hidden bg-red-50">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-2.5 py-1.5 hover:bg-red-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <AlertTriangle className="w-3.5 h-3.5 text-red-500 shrink-0" />
          <span className="font-medium text-xs text-red-800">Escalation</span>
          {timeoutSeconds && !isResolved && !timedOut && (
            <span className="text-xs text-gray-500">
              (タイムアウト: {formatTimeout(timeoutSeconds)})
            </span>
          )}
          {timedOut && !isResolved && (
            <span className="text-xs text-amber-600 bg-amber-50 border border-amber-200 rounded px-1.5 py-0.5">
              タイムアウト - エージェントは自動続行しました
            </span>
          )}
          {isResolved && <CheckCircle2 className="w-3 h-3 text-green-500 ml-auto shrink-0" />}
        </div>
      </button>
      {expanded && (
        <div className="px-3 pb-3 space-y-2">
          <p className="text-sm text-gray-800">{inp?.message}</p>
          {isResolved ? (
            <div className="text-xs text-gray-500 bg-white border border-gray-200 rounded px-2 py-1.5">
              <span className="text-gray-400">Response: </span>
              {item.result}
            </div>
          ) : timedOut ? (
            <div className="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded px-2 py-1.5">
              タイムアウト - エージェントは自動続行しました
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex gap-2">
              <input
                type="text"
                value={response}
                onChange={(e) => setResponse(e.target.value)}
                placeholder="Enter your response..."
                className="flex-1 border border-gray-300 rounded px-2 py-1.5 text-xs"
                disabled={sending}
              />
              <button
                type="submit"
                disabled={sending || !response.trim()}
                className="px-3 py-1.5 text-xs bg-red-600 text-white rounded hover:bg-red-700 disabled:opacity-50"
              >
                {sending ? "..." : "Respond"}
              </button>
            </form>
          )}
        </div>
      )}
    </div>
  );
}

function ToolCallView({ item, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded }: {
  item: Extract<StreamDisplayItem, { kind: "tool_call" }>;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
}) {
  if (item.name === "TodoWrite") {
    return <TodoCallView item={item} />;
  }
  if (item.name === "AskUserQuestion") {
    return <AskUserQuestionCallView item={item} onAnswer={onAnswer} />;
  }
  if (item.name === "mcp__agmux__escalate") {
    return <EscalateCallView item={item} sessionId={sessionId} escalationId={escalationId} timedOut={escalationTimedOut} timeoutSeconds={escalationTimeoutSeconds} onResponded={onEscalationResponded} />;
  }

  const [open, setOpen] = useState(false);
  const Icon = toolIcon(item.name);
  const desc = toolDescription(item.name, item.input);
  const subDetail = toolSubDetail(item.name, item.input);
  const done = item.result !== undefined;
  const hasChildren = item.children && item.children.length > 0;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2">
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
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><Icon className="w-4 h-4 text-gray-500" />{item.name} {desc && <span className="text-gray-400 font-normal">{desc}</span>}</span>}
      >
        <div className="space-y-3">
          <ToolInputView input={item.input} />
          {item.resultImages && item.resultImages.length > 0 && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Images</span>
              <div className="mt-1 flex flex-wrap gap-2">
                {item.resultImages.map((img, i) => (
                  <img
                    key={i}
                    src={`data:${img.mediaType};base64,${img.data}`}
                    alt={`result-${i}`}
                    className="max-w-full max-h-96 rounded border border-gray-200"
                  />
                ))}
              </div>
            </div>
          )}
          {done && item.result && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
              <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{item.result.slice(0, 2000)}</pre>
            </div>
          )}
        </div>
      </Modal>
      {hasChildren && (
        <div className="ml-4 border-l-2 border-indigo-200 pl-3 space-y-1.5">
          {item.children!.map((child, i) => (
            <div key={i}>
              {child.kind === "tool_call" ? (
                <ToolCallView item={child} />
              ) : child.kind === "text" ? (
                <CollapsibleText text={child.text} />
              ) : null}
            </div>
          ))}
        </div>
      )}
    </>
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

function StreamDisplayItemView({ item, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded }: {
  item: StreamDisplayItem;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
}) {
  if (item.kind === "text") {
    return <CollapsibleText text={item.text} />;
  }
  if (item.kind === "image") {
    return (
      <img
        src={`data:${item.mediaType};base64,${item.data}`}
        alt="attached"
        className="max-w-xs max-h-48 rounded border border-gray-200"
      />
    );
  }
  if (item.kind === "tool_call") {
    return <ToolCallView item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />;
  }
  if (item.kind === "system_event") {
    return <SystemEventView item={item} />;
  }
  return null;
}

// --- Todo extraction from stream ---

interface TodoItem {
  content: string;
  status: "pending" | "in_progress" | "completed";
  activeForm: string;
}

function parseTodoInput(input: unknown): TodoItem[] | null {
  const inp = input as { todos?: TodoItem[] } | null;
  if (inp?.todos && Array.isArray(inp.todos)) return inp.todos;
  return null;
}

function TodoCallView({ item }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }> }) {
  const [open, setOpen] = useState(false);
  const todos = parseTodoInput(item.input);
  if (!todos || todos.length === 0) return null;

  const completed = todos.filter(t => t.status === "completed").length;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2 mb-1.5">
          <ListTodo className="w-3.5 h-3.5 text-gray-400" />
          <span className="font-medium text-xs text-gray-800">Tasks</span>
          <span className="text-[10px] text-gray-400 ml-auto">{completed}/{todos.length}</span>
        </div>
        <div className="space-y-0.5">
          {todos.map((todo, i) => (
            <div key={i} className="flex items-start gap-1.5 text-xs">
              {todo.status === "completed" ? (
                <CheckCircle2 className="w-3.5 h-3.5 text-green-500 shrink-0 mt-0.5" />
              ) : todo.status === "in_progress" ? (
                <Circle className="w-3.5 h-3.5 text-blue-500 fill-blue-500 shrink-0 mt-0.5" />
              ) : (
                <Circle className="w-3.5 h-3.5 text-gray-300 shrink-0 mt-0.5" />
              )}
              <span className={todo.status === "completed" ? "text-gray-400 line-through" : "text-gray-700"}>
                {todo.content}
              </span>
            </div>
          ))}
        </div>
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><ListTodo className="w-4 h-4 text-gray-500" />TodoWrite</span>}
      >
        <div className="space-y-3">
          <ToolInputView input={item.input} />
          {item.result !== undefined && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
              <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{item.result!.slice(0, 2000)}</pre>
            </div>
          )}
        </div>
      </Modal>
    </>
  );
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

function StreamOutputView({ lines, className, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded }: {
  lines: unknown[];
  className?: string;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
}) {
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
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />
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
                    <StreamDisplayItemView key={j} item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />
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
        onClick={() => setOpen(true)}
        className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-orange-50 text-orange-700 hover:bg-orange-100"
        title="Changes"
      >
        <FileDiff className="w-3 h-3" />
        {files.length}
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><FileDiff className="w-4 h-4 text-orange-500" />Changes ({files.length} files)</span>}
      >
        <div className="border border-gray-200 rounded-lg overflow-hidden">
          {files.map((file) => (
            <div key={file.path} className="border-b border-gray-100 last:border-b-0">
              <button
                onClick={() => toggle(file.path)}
                className="w-full flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-gray-50 text-left min-w-0"
              >
                <span className={`px-1.5 py-0.5 rounded font-mono text-[10px] font-bold shrink-0 ${statusBadgeColor[file.status] || "bg-gray-100 text-gray-600"}`}>
                  {file.status}
                </span>
                <span className="font-mono text-gray-700 truncate min-w-0">{file.path}</span>
                {file.diff && (
                  <span className="ml-auto text-gray-400 shrink-0">{expanded.has(file.path) ? "▼" : "▶"}</span>
                )}
              </button>
              {expanded.has(file.path) && file.diff && (
                <pre className="bg-gray-50 text-gray-800 text-xs p-3 overflow-x-auto whitespace-pre font-mono border-t border-gray-200">
                  {file.diff.split("\n").map((line, i) => (
                    <span
                      key={i}
                      className={
                        line.startsWith("+") && !line.startsWith("+++")
                          ? "text-green-700 bg-green-50"
                          : line.startsWith("-") && !line.startsWith("---")
                            ? "text-red-700 bg-red-50"
                            : line.startsWith("@@")
                              ? "text-blue-600"
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
      </Modal>
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
  const [pendingImages, setPendingImages] = useState<{ data: string; mediaType: string; preview: string }[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [pendingEscalationId, setPendingEscalationId] = useState<string | null>(null);
  const [escalationTimedOut, setEscalationTimedOut] = useState(false);
  const [escalationTimeoutSeconds, setEscalationTimeoutSeconds] = useState(300);
  const [reconnectToast, setReconnectToast] = useState(false);
  const [clearToast, setClearToast] = useState<"success" | "error" | null>(null);
  const [slashCommands, setSlashCommands] = useState<string[]>([]);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [showActionMenu, setShowActionMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState("");
  const [slashSelectedIndex, setSlashSelectedIndex] = useState(0);
  const [claudeMDOpen, setClaudeMDOpen] = useState(false);
  const [claudeMDContent, setClaudeMDContent] = useState<string | null>(null);
  const [claudeMDLoading, setClaudeMDLoading] = useState(false);
  const terminal = useAutoScroll(output);
  const streamCursorRef = useRef<number | null>(null);

  // Extract slash_commands from system init messages
  useEffect(() => {
    for (const line of streamLines) {
      const entry = line as Record<string, unknown>;
      if (entry.type === "system" && entry.subtype === "init") {
        const cmds = entry.slash_commands as string[] | undefined;
        if (cmds && cmds.length > 0) {
          setSlashCommands(cmds);
        }
        break;
      }
    }
  }, [streamLines]);

  // Track active session name for notification suppression
  useEffect(() => {
    return () => {
      setActiveSessionName(null);
    };
  }, []);

  // Listen for escalation WebSocket messages
  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "escalation") {
      const data = msg.data as { id: string; sessionId: string; timeoutSeconds?: number };
      if (data.sessionId === sessionId) {
        setPendingEscalationId(data.id);
        setEscalationTimedOut(false);
        setEscalationTimeoutSeconds(data.timeoutSeconds ?? 300);
      }
    }
    if (msg.type === "escalation_timeout") {
      const data = msg.data as { id: string; sessionId: string };
      if (data.sessionId === sessionId) {
        setEscalationTimedOut(true);
      }
    }
  }, [sessionId]);
  useWebSocket(handleWsMessage);

  const MAX_IMAGE_SIZE = 5 * 1024 * 1024; // 5MB
  const ALLOWED_TYPES = ["image/jpeg", "image/png", "image/gif", "image/webp"];

  const addImageFiles = (files: FileList | File[]) => {
    Array.from(files).forEach((file) => {
      if (!ALLOWED_TYPES.includes(file.type)) {
        alert(`Unsupported format: ${file.type}. Supported: JPEG, PNG, GIF, WebP`);
        return;
      }
      if (file.size > MAX_IMAGE_SIZE) {
        alert(`File too large: ${(file.size / 1024 / 1024).toFixed(1)}MB. Max: 5MB`);
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        const result = reader.result as string;
        const base64 = result.split(",")[1];
        setPendingImages((prev) => [
          ...prev,
          { data: base64, mediaType: file.type, preview: result },
        ]);
      };
      reader.readAsDataURL(file);
    });
  };

  const removeImage = (index: number) => {
    setPendingImages((prev) => prev.filter((_, i) => i !== index));
  };

  useEffect(() => {
    if (!sessionId) return;
    // Reset cursor on session change
    streamCursorRef.current = null;

    api.getSession(sessionId).then((s) => {
      setSession(s);
      setActiveSessionName(s.name);
      if (s.outputMode === "stream") {
        api.getStreamOutput(sessionId).then((resp) => {
          setStreamLines(resp.lines);
          streamCursorRef.current = resp.total;
        }).catch(() => {});
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    });
    api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
    api.getPendingEscalation(sessionId).then((r) => {
      if (r.escalation) {
        setPendingEscalationId(r.escalation.id);
        setEscalationTimedOut(r.escalation.timedOut ?? false);
        setEscalationTimeoutSeconds(r.escalation.timeoutSeconds ?? 300);
      }
    }).catch(() => {});

    const interval = setInterval(() => {
      api.getSession(sessionId).then((s) => {
        setSession(s);
        if (s.outputMode === "stream") {
          const cursor = streamCursorRef.current;
          if (cursor !== null) {
            // Delta fetch: only get new lines
            api.getStreamOutputDelta(sessionId, cursor).then((resp) => {
              if (resp.lines.length > 0) {
                setStreamLines((prev) => [...prev, ...resp.lines]);
              }
              streamCursorRef.current = resp.total;
            }).catch(() => {});
          } else {
            // Fallback to full fetch if cursor not initialized
            api.getStreamOutput(sessionId).then((resp) => {
              setStreamLines(resp.lines);
              streamCursorRef.current = resp.total;
            }).catch(() => {});
          }
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
    if (!sessionId || (!message.trim() && pendingImages.length === 0)) return;
    const images = pendingImages.length > 0
      ? pendingImages.map(({ data, mediaType }) => ({ data, mediaType }))
      : undefined;
    await api.sendToSession(sessionId, message, images);
    setMessage("");
    setPendingImages([]);
    setTimeout(() => {
      if (session?.outputMode === "stream") {
        const cursor = streamCursorRef.current;
        if (cursor !== null) {
          api.getStreamOutputDelta(sessionId, cursor).then((resp) => {
            if (resp.lines.length > 0) {
              setStreamLines((prev) => [...prev, ...resp.lines]);
            }
            streamCursorRef.current = resp.total;
          }).catch(() => {});
        } else {
          api.getStreamOutput(sessionId).then((resp) => {
            setStreamLines(resp.lines);
            streamCursorRef.current = resp.total;
          }).catch(() => {});
        }
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    }, 500);
  };

  const isStream = session?.outputMode === "stream";

  const sendForm = session ? (
    <form onSubmit={handleSend} className="shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8 border-t border-gray-100">
      {pendingImages.length > 0 && (
        <div className="flex gap-2 mb-2 flex-wrap">
          {pendingImages.map((img, i) => (
            <div key={i} className="relative group">
              <img
                src={img.preview}
                alt={`preview ${i}`}
                className="w-16 h-16 object-cover rounded border border-gray-200"
              />
              <button
                type="button"
                onClick={() => removeImage(i)}
                className="absolute -top-1.5 -right-1.5 w-4 h-4 bg-red-500 text-white rounded-full flex items-center justify-center text-[10px]"
              >
                <X className="w-2.5 h-2.5" />
              </button>
            </div>
          ))}
        </div>
      )}
      <div className="flex gap-2 items-center">
        {/* Left action menu */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setShowActionMenu((v) => !v)}
            className="w-9 h-9 flex items-center justify-center text-gray-500 bg-gray-50 rounded-full hover:bg-gray-100"
            title="Actions"
          >
            <Plus className="w-4 h-4" />
          </button>
          {showActionMenu && (
            <div className="absolute bottom-full left-0 mb-1 bg-white border border-gray-200 rounded-lg shadow-lg z-50 min-w-[160px]">
              <button
                type="button"
                className="w-full text-left px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 flex items-center gap-2"
                onMouseDown={(e) => {
                  e.preventDefault();
                  fileInputRef.current?.click();
                  setShowActionMenu(false);
                }}
              >
                <ImagePlus className="w-4 h-4" /> Add image
              </button>
              {slashCommands.length > 0 && (
                <button
                  type="button"
                  className="w-full text-left px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 flex items-center gap-2"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    setMessage("/");
                    setSlashFilter("");
                    setSlashSelectedIndex(0);
                    setShowSlashMenu(true);
                    setShowActionMenu(false);
                  }}
                >
                  <Slash className="w-4 h-4" /> Slash commands
                </button>
              )}
              {session.status !== "stopped" && session.type !== "controller" && (
                <button
                  type="button"
                  className="w-full text-left px-3 py-2 text-sm text-red-600 hover:bg-red-50 flex items-center gap-2"
                  onMouseDown={async (e) => {
                    e.preventDefault();
                    setShowActionMenu(false);
                    await api.stopSession(session.id);
                    api.getSession(session.id).then(setSession);
                  }}
                >
                  <Square className="w-4 h-4" /> Stop session
                </button>
              )}
            </div>
          )}
        </div>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/jpeg,image/png,image/gif,image/webp"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files) addImageFiles(e.target.files);
            e.target.value = "";
          }}
        />
        {/* Message input with slash command popup */}
        <div className="relative flex-1">
          {showSlashMenu && (() => {
            const filtered = slashCommands.filter((cmd) =>
              slashFilter === "" || cmd.toLowerCase().includes(slashFilter.toLowerCase())
            );
            if (filtered.length === 0) return null;
            return (
              <div className="absolute bottom-full left-0 right-0 mb-1 bg-white border border-gray-200 rounded-lg shadow-lg max-h-60 overflow-y-auto z-50">
                {filtered.map((cmd, i) => (
                  <button
                    key={cmd}
                    type="button"
                    className={`w-full text-left px-3 py-2 text-sm hover:bg-blue-50 ${
                      i === slashSelectedIndex ? "bg-blue-100 text-blue-800" : "text-gray-700"
                    }`}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      setMessage(`/${cmd} `);
                      setShowSlashMenu(false);
                    }}
                  >
                    /{cmd}
                  </button>
                ))}
              </div>
            );
          })()}
          <input
            type="text"
            value={message}
            onChange={(e) => {
              const val = e.target.value;
              setMessage(val);
              if (val.startsWith("/") && slashCommands.length > 0) {
                const filter = val.slice(1).split(" ")[0];
                if (!val.includes(" ") || val === "/") {
                  setSlashFilter(filter);
                  setSlashSelectedIndex(0);
                  setShowSlashMenu(true);
                } else {
                  setShowSlashMenu(false);
                }
              } else {
                setShowSlashMenu(false);
              }
            }}
            onKeyDown={(e) => {
              if (!showSlashMenu) return;
              const filtered = slashCommands.filter((cmd) =>
                slashFilter === "" || cmd.toLowerCase().includes(slashFilter.toLowerCase())
              );
              if (filtered.length === 0) return;
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSlashSelectedIndex((prev) => Math.min(prev + 1, filtered.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSlashSelectedIndex((prev) => Math.max(prev - 1, 0));
              } else if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                setMessage(`/${filtered[slashSelectedIndex]} `);
                setShowSlashMenu(false);
              } else if (e.key === "Escape") {
                setShowSlashMenu(false);
              }
            }}
            onBlur={() => { setShowSlashMenu(false); setShowActionMenu(false); }}
            onPaste={(e) => {
              const items = e.clipboardData?.items;
              if (!items) return;
              const imageFiles: File[] = [];
              for (const item of Array.from(items)) {
                if (item.type.startsWith("image/")) {
                  const file = item.getAsFile();
                  if (file) imageFiles.push(file);
                }
              }
              if (imageFiles.length > 0) {
                e.preventDefault();
                addImageFiles(imageFiles);
              }
            }}
            placeholder="Send a message..."
            className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
          />
        </div>
        {/* Send button */}
        <button
          type="submit"
          className="w-9 h-9 flex items-center justify-center bg-blue-600 text-white rounded-full hover:bg-blue-700"
        >
          <SendHorizonal className="w-4 h-4" />
        </button>
      </div>
    </form>
  ) : null;

  return (
    <div className="h-dvh flex flex-col px-4 sm:px-8 pt-4 sm:pt-8 max-w-4xl mx-auto">
      {reconnectToast && (
        <div className="fixed top-4 right-4 z-50 bg-green-600 text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 animate-fade-in">
          <CheckCircle2 className="w-4 h-4" />
          再接続に成功しました
        </div>
      )}
      {clearToast === "success" && (
        <div className="fixed top-4 right-4 z-50 bg-green-600 text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 animate-fade-in">
          <CheckCircle2 className="w-4 h-4" />
          セッションをクリアしました
        </div>
      )}
      {clearToast === "error" && (
        <div className="fixed top-4 right-4 z-50 bg-red-600 text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 animate-fade-in">
          <AlertTriangle className="w-4 h-4" />
          クリアに失敗しました
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2 sm:gap-3 mb-3 shrink-0">
        <button
          onClick={() => navigate("/")}
          className="p-1 text-gray-400 hover:text-gray-700 rounded hover:bg-gray-100 shrink-0"
          title="Back"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        {session ? (
          <>
            <StatusDot status={session.status} />
            <h2 className="text-xl sm:text-2xl font-bold">{session.name}</h2>
            <span className="text-xs text-gray-400">{session.status}</span>
          </>
        ) : (
          <>
            <div className="w-3 h-3 rounded-full bg-gray-200 animate-pulse" />
            <div className="h-7 w-40 bg-gray-200 rounded animate-pulse" />
          </>
        )}
        {session && session.type !== "controller" && (
          <div className="flex gap-1.5 sm:ml-auto">
            <button
              onClick={async () => {
                if (!confirm("Clear session context? This will start a fresh conversation.")) return;
                try {
                  await api.clearSession(session.id);
                  // Optimistic UI update: immediately clear local state
                  setStreamLines([]);
                  setOutput("");
                  streamCursorRef.current = 0;
                  // Refresh session metadata
                  api.getSession(session.id).then(setSession);
                  setClearToast("success");
                  setTimeout(() => setClearToast(null), 3000);
                } catch {
                  setClearToast("error");
                  setTimeout(() => setClearToast(null), 3000);
                }
              }}
              className="p-1.5 text-orange-700 bg-orange-50 rounded hover:bg-orange-100"
              title="Clear"
            >
              <RotateCcw className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={async () => {
                if (!confirm("セッションを再接続しますか？")) return;
                try {
                  await api.reconnectSession(session.id);
                  api.getSession(session.id).then(setSession);
                  setReconnectToast(true);
                  setTimeout(() => setReconnectToast(false), 3000);
                } catch {
                  alert("再接続に失敗しました");
                }
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
      {session ? (
        <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm text-gray-500">
          <FolderOpen className="w-3.5 h-3.5 text-gray-400 shrink-0" />
          <span className="truncate" title={session.projectPath}>{session.projectPath}</span>
        {session.githubUrl && (
          <a
            href={session.githubUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-gray-400 hover:text-gray-700 shrink-0 ml-0.5"
            title="Open on GitHub"
          >
            <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
          </a>
        )}
        <button
          onClick={() => {
            setClaudeMDOpen(true);
            if (claudeMDContent === null && !claudeMDLoading) {
              setClaudeMDLoading(true);
              api.getClaudeMD(session.id).then((res) => {
                setClaudeMDContent(res.content);
              }).catch(() => {
                setClaudeMDContent("CLAUDE.md not found");
              }).finally(() => {
                setClaudeMDLoading(false);
              });
            }
          }}
          className="text-gray-400 hover:text-purple-600 shrink-0 ml-0.5"
          title="Show CLAUDE.md"
        >
          <Sparkles className="w-3.5 h-3.5" />
        </button>
        </div>
      ) : (
        <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm">
          <FolderOpen className="w-3.5 h-3.5 text-gray-300 shrink-0" />
          <div className="h-4 w-60 bg-gray-200 rounded animate-pulse" />
        </div>
      )}
      {session && (session.branch || (session.pullRequests && session.pullRequests.length > 0) || diffFiles.length > 0) && (
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

      {session && (session.currentTask || session.goal) && (
        <div className="border-l-2 border-gray-300 bg-gray-50 rounded-r-lg px-3 py-2 mb-3 space-y-1">
          {session.goals && session.goals.length > 1 && (
            <div className="text-[10px] text-gray-400 ml-5">
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
            <div className="flex items-start gap-1.5 text-xs text-gray-700">
              <ListTodo className="w-3.5 h-3.5 shrink-0 mt-0.5 text-indigo-400" />
              <span>{session.currentTask}</span>
            </div>
          )}
          {session.goal && (
            <div className="flex items-start gap-1.5 text-xs text-gray-500">
              <Target className="w-3.5 h-3.5 shrink-0 mt-0.5 text-emerald-400" />
              <span>{session.goal}</span>
            </div>
          )}
        </div>
      )}

      {!session ? (
        <div className="flex flex-col flex-1 min-h-0 items-center justify-center">
          <div className="space-y-3 w-full max-w-2xl px-4">
            <div className="h-4 bg-gray-200 rounded animate-pulse w-3/4" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-full" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-5/6" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-2/3" />
          </div>
        </div>
      ) : isStream ? (
        <div className="flex flex-col flex-1 min-h-0">
          <StreamOutputView lines={streamLines} className="flex-1 min-h-0" sessionId={sessionId} escalationId={pendingEscalationId ?? undefined} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={() => { setPendingEscalationId(null); setEscalationTimedOut(false); }} onAnswer={async (text) => {
            if (!sessionId) return;
            await api.sendToSession(sessionId, text);
          }} />
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

      {/* CLAUDE.md Modal */}
      {claudeMDOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setClaudeMDOpen(false)}>
          <div className="bg-white rounded-xl shadow-2xl w-full max-w-2xl max-h-[80vh] flex flex-col" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-3 border-b">
              <div className="flex items-center gap-2">
                <Sparkles className="w-4 h-4 text-purple-500" />
                <span className="font-semibold text-sm">CLAUDE.md</span>
              </div>
              <button onClick={() => setClaudeMDOpen(false)} className="text-gray-400 hover:text-gray-700">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="overflow-y-auto p-5 prose prose-sm max-w-none">
              {claudeMDLoading ? (
                <div className="text-gray-400 text-sm">Loading...</div>
              ) : claudeMDContent ? (
                <Markdown remarkPlugins={[remarkGfm]}>{claudeMDContent}</Markdown>
              ) : (
                <div className="text-gray-400 text-sm">No content</div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
