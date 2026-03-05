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
    outputMode?: "terminal" | "stream";
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

  getStreamOutput: (id: string, limit = 200) =>
    request<unknown[]>(`/sessions/${id}/stream?limit=${limit}`),

  updateSessionContext: (id: string, data: { currentTask: string; goal: string }) =>
    request<{ status: string }>(`/sessions/${id}/context`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  reconnectSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/reconnect`, { method: "POST" }),

  restartController: () =>
    request<Session>("/sessions/controller/restart", { method: "POST" }),

  getDiff: (id: string) =>
    request<{ files: DiffFile[] }>(`/sessions/${id}/diff`),

  getConfig: () => request<AppConfig>("/config"),

  updateConfig: (data: AppConfig) =>
    request<{ status: string }>("/config", {
      method: "PUT",
      body: JSON.stringify(data),
    }),
};

export interface DiffFile {
  path: string;
  status: string;
  diff: string;
}

export interface LogEntry {
  time: string;
  level: string;
  msg: string;
  component?: string;
  session?: string;
  sessionId?: string;
  [key: string]: unknown;
}

export interface AppConfig {
  server: { port: number };
  daemon: { interval: string };
  session: { claudeCommand: string };
  prompts?: { statusCheck: string };
}
