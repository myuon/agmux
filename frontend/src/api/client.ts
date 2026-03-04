import type { Session } from "../types/session";

const BASE = "/api";

async function request<T>(
  path: string,
  options?: RequestInit
): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }
  return res.json();
}

export const api = {
  listSessions: () => request<Session[]>("/sessions"),

  createSession: (data: {
    name: string;
    projectPath: string;
    prompt?: string;
  }) =>
    request<Session>("/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  getSession: (id: string) => request<Session>(`/sessions/${id}`),

  stopSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/stop`, { method: "POST" }),

  deleteSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}`, { method: "DELETE" }),

  sendToSession: (id: string, text: string) =>
    request<{ status: string }>(`/sessions/${id}/send`, {
      method: "POST",
      body: JSON.stringify({ text }),
    }),

  getSessionOutput: (id: string) =>
    request<{ output: string }>(`/sessions/${id}/output`),

  getLogs: (limit = 100) =>
    request<LogEntry[]>(`/logs?limit=${limit}`),

  getSessionLogs: (id: string) =>
    request<ClaudeLogEntry[]>(`/sessions/${id}/logs`),

  restartController: () =>
    request<Session>("/sessions/controller/restart", { method: "POST" }),

  getConfig: () => request<AppConfig>("/config"),

  updateConfig: (data: AppConfig) =>
    request<{ status: string }>("/config", {
      method: "PUT",
      body: JSON.stringify(data),
    }),
};

export interface LogEntry {
  time: string;
  level: string;
  msg: string;
  component?: string;
  session?: string;
  sessionId?: string;
  [key: string]: unknown;
}

export interface ClaudeContentBlock {
  type: "text" | "tool_use" | "tool_result";
  text?: string;
  name?: string;
  input?: unknown;
  content?: string;
}

export interface ClaudeLogEntry {
  type: "user" | "assistant";
  timestamp: string;
  blocks: ClaudeContentBlock[];
}

export interface AppConfig {
  server: { port: number };
  daemon: { interval: string };
  session: { claudeCommand: string };
}
