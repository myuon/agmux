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
    case "Agent": return Bot;
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
  if (name === "Agent" && inp && "description" in inp) {
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
