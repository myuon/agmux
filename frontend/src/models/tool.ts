import {
  Terminal, FileText, FilePen, PenLine, Search, Sparkles, Globe, Wrench, Bot, AlertTriangle,
} from "lucide-react";
import type { AskUserQuestionItem } from "./stream";

export function toolIcon(name: string) {
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
    case "Agent":
    case "Task": return Bot;
    case "mcp__agmux__escalate": return AlertTriangle;
    default: return Wrench;
  }
}

export function toolDescription(name: string, input: unknown): string | null {
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
  if ((name === "Agent" || name === "Task") && inp && "description" in inp) {
    return String(inp.description);
  }
  if (name === "AskUserQuestion" && inp && "questions" in inp && Array.isArray(inp.questions)) {
    const questions = inp.questions as AskUserQuestionItem[];
    return questions[0]?.question?.slice(0, 60) || null;
  }
  if (name === "mcp__agmux__escalate" && inp && "message" in inp) {
    return String(inp.message).slice(0, 80);
  }
  return null;
}

// Category label used to summarize a group of tool calls.
export function toolCategoryLabel(name: string): string {
  switch (name) {
    case "Bash": return "コマンド実行";
    case "Read": return "ファイル読み込み";
    case "Grep":
    case "Glob": return "検索";
    case "Edit":
    case "Write":
    case "NotebookEdit": return "ファイル編集";
    case "WebFetch":
    case "WebSearch": return "Web";
    case "Agent":
    case "Task": return "サブエージェント";
  }
  // mcp__<server>__<tool> -> <tool>
  if (name.startsWith("mcp__")) {
    const parts = name.split("__");
    return parts[parts.length - 1] || name;
  }
  return name;
}

// "コマンド実行 3 · ファイル読み込み 4" (in order of first appearance)
export function summarizeToolCategories(names: string[]): string {
  const counts = new Map<string, number>();
  for (const n of names) {
    const label = toolCategoryLabel(n);
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }
  return Array.from(counts, ([label, count]) => `${label} ${count}`).join(" · ");
}

export function toolSubDetail(name: string, input: unknown): string | null {
  const inp = input && typeof input === "object" ? (input as Record<string, unknown>) : null;
  if (name === "Bash" && inp && "command" in inp) {
    const cmd = String(inp.command).split("\n")[0].slice(0, 120);
    // descriptionがある場合のみコマンドをサブ詳細として表示
    if ("description" in inp && inp.description) return `$ ${cmd}`;
  }
  return null;
}

// --- Todo model ---

export interface TodoItem {
  content: string;
  status: "pending" | "in_progress" | "completed";
  activeForm: string;
}

export function parseTodoInput(input: unknown): TodoItem[] | null {
  const inp = input as { todos?: TodoItem[] } | null;
  if (inp?.todos && Array.isArray(inp.todos)) return inp.todos;
  return null;
}

const HANDBACK_MARKER = "[Subagent hand-back]";
const HANDBACK_REPORT_START = "The report follows:";
const REPORT_INDENT = "  ";

/**
 * Claude Code wraps an Agent/Task tool_result in a "[Subagent hand-back]"
 * frame: an optional `<tools-used-before-summary>` note, a fixed preamble that
 * ends with "The report follows:", then the report with every line indented by
 * two spaces. Trailers such as `agentId: ...` and `<usage>` follow at column
 * zero. This strips the note and preamble, un-indents the report and keeps the
 * trailers. Results without the frame are returned unchanged.
 */
export function stripSubagentHandback(result: string): string {
  const lines = result.split("\n");
  const markerIdx = lines.findIndex((l) => l.startsWith(HANDBACK_MARKER));
  if (markerIdx < 0) return result;
  const startIdx = lines.findIndex((l, i) => i >= markerIdx && l.includes(HANDBACK_REPORT_START));
  if (startIdx < 0) return result;
  const head = lines[startIdx];
  const inline = head.slice(head.indexOf(HANDBACK_REPORT_START) + HANDBACK_REPORT_START.length).trim();

  const report: string[] = inline ? [inline] : [];
  let i = startIdx + 1;
  for (; i < lines.length; i++) {
    const l = lines[i];
    if (l.startsWith(REPORT_INDENT)) report.push(l.slice(REPORT_INDENT.length));
    else if (l.trim() === "") report.push("");
    else break;
  }
  const rest = lines.slice(i).join("\n");
  const body = report.join("\n").trim();
  return rest ? `${body}\n\n${rest}` : body;
}
