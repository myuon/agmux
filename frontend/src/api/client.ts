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
    provider?: string;
    model?: string;
    autoApprove?: boolean;
  }) =>
    request<Session>("/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  getCodexModels: () =>
    request<CodexModel[]>("/codex/models"),

  getClaudeModels: () =>
    request<{ id: string; name: string; default?: boolean }[]>("/claude/models"),

  getClaudeVersion: () =>
    request<{ version: string }>("/claude/version"),

  getCodexVersion: () =>
    request<{ version: string }>("/codex/version"),

  getSession: (id: string) => request<Session>(`/sessions/${id}`),

  stopSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/stop`, { method: "POST" }),

  deleteSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}`, { method: "DELETE" }),

  duplicateSession: (id: string) =>
    request<Session>(`/sessions/${id}/duplicate`, { method: "POST" }),

  sendToSession: (id: string, text: string, images?: { data: string; mediaType: string }[]) =>
    request<{ status: string }>(`/sessions/${id}/send`, {
      method: "POST",
      body: JSON.stringify({ text, ...(images && images.length > 0 ? { images } : {}) }),
    }),

  getSessionOutput: (id: string) =>
    request<{ output: string }>(`/sessions/${id}/output`),

  getLogs: (limit = 100) =>
    request<LogEntry[]>(`/logs?limit=${limit}`),

  getStreamOutput: (id: string, limit = 200) =>
    request<{ lines: unknown[]; total: number }>(`/sessions/${id}/stream?limit=${limit}`),

  getStreamOutputDelta: (id: string, after: number) =>
    request<{ lines: unknown[]; total: number }>(`/sessions/${id}/stream?after=${after}`),

  updateSessionContext: (id: string, data: { currentTask: string; goal: string }) =>
    request<{ status: string }>(`/sessions/${id}/context`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  reconnectSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/reconnect`, { method: "POST" }),

  clearSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/clear`, { method: "POST" }),

  restartController: () =>
    request<Session>("/sessions/controller/restart", { method: "POST" }),

  getDiff: (id: string) =>
    request<{ files: DiffFile[] }>(`/sessions/${id}/diff`),

  getClaudeMD: (id: string) =>
    request<{ files: { path: string; content: string }[] }>(`/sessions/${id}/claude-md`),

  getSettingsJSON: (id: string) =>
    request<{ files: { name: string; content: string }[] }>(`/sessions/${id}/settings-json`),

  getPendingEscalation: (sessionId: string) =>
    request<{ escalation: { id: string; sessionId: string; message: string; timedOut?: boolean; timeoutSeconds?: number } | null }>(`/sessions/${sessionId}/escalate`),

  respondEscalation: (sessionId: string, escalationId: string, response: string) =>
    request<{ status: string }>(`/sessions/${sessionId}/escalate/respond`, {
      method: "POST",
      body: JSON.stringify({ id: escalationId, response }),
    }),

  getConfig: () => request<AppConfig>("/config"),

  updateConfig: (data: AppConfig) =>
    request<{ status: string }>("/config", {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  getMetrics: (params?: { name?: string; session_id?: string; since?: string }) => {
    const qs = new URLSearchParams();
    if (params?.name) qs.set("name", params.name);
    if (params?.session_id) qs.set("session_id", params.session_id);
    if (params?.since) qs.set("since", params.since);
    return request<MetricRow[]>(`/metrics?${qs}`);
  },

  getMetricsSummary: (since?: string) => {
    const qs = since ? `?since=${since}` : "";
    return request<MetricsSummary>(`/metrics/summary${qs}`);
  },

  getMetricsEvents: (params?: { name?: string; session_id?: string; since?: string }) => {
    const qs = new URLSearchParams();
    if (params?.name) qs.set("name", params.name);
    if (params?.session_id) qs.set("session_id", params.session_id);
    if (params?.since) qs.set("since", params.since);
    return request<MetricEvent[]>(`/metrics/events?${qs}`);
  },
};

export interface CodexModel {
  id: string;
  name: string;
  description?: string;
  isDefault?: boolean;
  reasoningEffort?: string;
}

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
  prompts?: { statusCheck: string; systemPrompt: string };
}

export interface MetricRow {
  id: number;
  name: string;
  value: number;
  attributes: Record<string, string>;
  sessionId: string;
  timestamp: string;
}

export interface MetricsSummary {
  totalCost: number;
  totalTokens: Record<string, number>;
  sessionCount: number;
  linesOfCode: number;
  activeTime: number;
  commitCount: number;
  pullRequestCount: number;
  codeEditDecisions: number;
  costBySession: { sessionId: string; cost: number }[];
  tokensBySession: { sessionId: string; input: number; output: number }[];
}

export interface MetricEvent {
  id: number;
  name: string;
  body: string;
  attributes: Record<string, string>;
  sessionId: string;
  timestamp: string;
}
