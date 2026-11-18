import type { Session } from "../types/session";
import type { SecretMeta, SecretRegisterBody, SecretRequestInfo } from "../lib/secret";

const BASE = "/api";

export interface Artifact {
  id: string;
  sessionId: string;
  title: string;
  createdAt: string;
  updatedAt: string;
}

// ChoiceCard mirrors `ChoiceCard` in internal/server/choice_cards.go.
export interface ChoiceCard {
  id: string;
  sessionId: string;
  prompt: string;
  choices: { id: string; label: string; action: { type: string } }[];
  source: "agent" | "rule";
  status: "open" | "answered" | "dismissed";
  answer?: string;
  streamOffset: number;
  createdAt: string;
}

export interface ExperimentalFeature {
  name: string;
  title: string;
  description: string;
  enabled: boolean;
}

export interface SessionSearchResult {
  sessionId: string;
  sessionName: string;
  projectPath: string;
  updatedAt: string;
  role: "user" | "assistant";
  snippet: string;
}

export interface LearningSignal {
  id: number;
  sessionId: string;
  sessionName?: string;
  kind: "correction" | "task";
  summary: string;
  excerpt: string;
  createdAt: string;
}

export interface LearningProposal {
  id: number;
  sessionId: string;
  sessionName: string;
  projectPath: string;
  target: "claude_md" | "skill" | "memory" | "loop";
  name: string;
  memoryType?: string;
  title: string;
  content: string;
  reason: string;
  status: "pending" | "applying" | "applied" | "rejected" | "failed";
  applySessionId?: string;
  error?: string;
  createdAt: string;
  decidedAt?: string;
  origin?: "repeated_correction" | "repeated_task";
  triggerType?: "interval" | "cron" | "queue";
  triggerValue?: string;
  automationId?: string;
  clusterId?: number;
  evidence?: LearningSignal[];
}

export interface SecretUsage {
  id: number;
  secretName: string;
  sessionId: string;
  projectPath: string;
  cwd: string;
  // argv[0] basename only; arguments are never recorded.
  command: string;
  argCount: number;
  exitCode?: number;
  status: "used" | "denied";
  usedAt: string;
}

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

  listForkSessions: (parentSessionId: string) =>
    request<Session[]>("/sessions").then((sessions) =>
      sessions.filter((s) => s.parentSessionId === parentSessionId)
    ),

  getRecentProjects: () =>
    request<RecentProject[]>("/projects/recent"),

  createSession: (data: {
    name: string;
    projectPath: string;
    prompt?: string;
    provider?: string;
    model?: string;
    autoApprove?: boolean;
    systemPrompt?: string;
    parentSessionId?: string;
    roleTemplate?: string;
    ephemeral?: boolean;
    ephemeralTimeoutSeconds?: number;
    autoName?: boolean;
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

  getAgmuxVersion: () =>
    request<{ version: string; commit: string; buildDate: string }>("/version"),

  getHostInfo: () => request<HostInfo>("/host-info"),

  getSession: (id: string) => request<Session>(`/sessions/${id}`),

  stopSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/stop`, { method: "POST" }),

  deleteSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}`, { method: "DELETE" }),

  duplicateSession: (id: string) =>
    request<Session>(`/sessions/${id}/duplicate`, { method: "POST" }),

  forkSession: (id: string, prompt: string, preserveContext: boolean = true) =>
    request<Session>(`/sessions/${id}/fork`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ preserveContext, prompt }),
    }),

  sendToSession: (id: string, text: string, images?: { data: string; mediaType: string }[]) =>
    request<{ status: string }>(`/sessions/${id}/send`, {
      method: "POST",
      body: JSON.stringify({ text, ...(images && images.length > 0 ? { images } : {}) }),
    }),

  execInSession: (id: string, command: string) =>
    request<{ exitCode: number }>(`/sessions/${id}/exec`, {
      method: "POST",
      body: JSON.stringify({ command }),
    }),

  // `start` is the index of lines[0] within the full (non-transient) history;
  // use it as the cursor for getStreamOutputBefore.
  getStreamOutput: (id: string, limit = 200) =>
    request<{ lines: unknown[]; total: number; start?: number }>(`/sessions/${id}/stream?limit=${limit}`),

  // Older history page: lines in [max(0, before - limit), before).
  getStreamOutputBefore: (id: string, before: number, limit = 300) =>
    request<{ lines: unknown[]; total: number; start: number }>(
      `/sessions/${id}/stream?before=${before}&limit=${limit}`
    ),

  // Full (non-truncated) stream lines of the subagent thread spawned by the
  // given Agent/Task tool_use id, including nested subagents.
  getSubagentStream: (id: string, parentToolUseId: string) =>
    request<{ lines: unknown[]; total: number }>(
      `/sessions/${id}/stream/subagent?parentToolUseId=${encodeURIComponent(parentToolUseId)}`
    ),

  getStreamOutputDelta: (id: string, after: number) =>
    request<{ lines: unknown[]; total: number }>(`/sessions/${id}/stream?after=${after}`),

  renameSession: (id: string, name: string) =>
    request<Session>(`/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),

  updateSessionContext: (id: string, data: { currentTask: string; goal: string }) =>
    request<{ status: string }>(`/sessions/${id}/context`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  updateSessionModel: (id: string, model: string) =>
    // While a turn is running the switch is only reserved: pendingModel is
    // then set and model stays unchanged until the turn completes.
    request<{ status: string; model?: string; pendingModel?: string }>(`/sessions/${id}/model`, {
      method: "PUT",
      body: JSON.stringify({ model }),
    }),

  reconnectSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/reconnect`, { method: "POST" }),

  restartSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/restart`, { method: "POST" }),

  clearSession: (id: string) =>
    request<{ status: string }>(`/sessions/${id}/clear`, { method: "POST" }),

  broadcastToSessions: (
    text: string,
    opts?: { sessionIds?: string[]; filter?: "active" | "all" }
  ) =>
    request<{
      results: { sessionId: string; status: string; error?: string }[];
    }>("/sessions/broadcast", {
      method: "POST",
      body: JSON.stringify({ text, ...opts }),
    }),

  restartController: () =>
    request<Session>("/sessions/controller/restart", { method: "POST" }),

  getDiff: (id: string) =>
    request<{ files: DiffFile[] }>(`/sessions/${id}/diff`),

  getClaudeMD: (id: string) =>
    request<{ files: { path: string; content: string }[] }>(`/sessions/${id}/claude-md`),

  getSettingsJSON: (id: string) =>
    request<{ files: { name: string; content: string }[] }>(`/sessions/${id}/settings-json`),

  getPendingEscalation: (sessionId: string) =>
    request<{ escalation: { id: string; sessionId: string; message: string; timedOut?: boolean; timeoutSeconds?: number; loginUrl?: string; secretRequest?: SecretRequestInfo } | null }>(`/sessions/${sessionId}/escalate`),

  /** Store the secret a request_secret escalation asked for and resolve it. */
  resolveSecretRequest: (sessionId: string, escalationId: string, body: SecretRegisterBody) =>
    request<SecretMeta>(`/sessions/${sessionId}/escalate/secret`, {
      method: "POST",
      body: JSON.stringify({ ...body, id: escalationId }),
    }),

  registerSecret: (body: SecretRegisterBody) =>
    request<SecretMeta>("/secrets", {
      method: "POST",
      body: JSON.stringify(body),
    }),

  respondEscalation: (sessionId: string, escalationId: string, response: string) =>
    request<{ status: string }>(`/sessions/${sessionId}/escalate/respond`, {
      method: "POST",
      body: JSON.stringify({ id: escalationId, response }),
    }),

  getPendingPermission: (sessionId: string) =>
    request<{ permission: { id: string; sessionId: string; toolName: string; input: unknown; timedOut?: boolean; timeoutSeconds?: number } | null }>(`/sessions/${sessionId}/permission`),

  respondPermission: (sessionId: string, permissionId: string, response: string) =>
    request<{ status: string }>(`/sessions/${sessionId}/permission/respond`, {
      method: "POST",
      body: JSON.stringify({ id: permissionId, response }),
    }),

  getConfig: () => request<AppConfig>("/config"),

  searchSessions: (q: string, limit = 30) =>
    request<{ results: SessionSearchResult[]; indexing: boolean }>(
      `/search?${new URLSearchParams({ q, limit: String(limit) })}`
    ),

  listLearningProposals: () =>
    request<{ proposals: LearningProposal[] }>("/learning/proposals"),

  acceptLearningProposal: (id: number, content?: string) =>
    request<{ proposal: LearningProposal }>(`/learning/proposals/${id}/accept`, {
      method: "POST",
      body: JSON.stringify(content !== undefined ? { content } : {}),
    }),

  rejectLearningProposal: (id: number) =>
    request<{ proposal: LearningProposal }>(`/learning/proposals/${id}/reject`, { method: "POST" }),

  reviewSessionLearning: (sessionId: string) =>
    request<{ started: boolean }>(`/sessions/${sessionId}/learning/review`, { method: "POST" }),

  listExperimental: () =>
    request<{ features: ExperimentalFeature[] }>("/experimental"),

  setExperimental: (name: string, enabled: boolean) =>
    request<{ features: ExperimentalFeature[] }>(`/experimental/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),

  // Experimental "choice_cards" only: 404 while the feature is off.
  listChoiceCards: (sessionId: string) =>
    request<{ cards: ChoiceCard[] }>(`/sessions/${sessionId}/choice-cards`),

  listOpenChoiceCards: () => request<{ cards: ChoiceCard[] }>("/choice-cards"),

  answerChoiceCard: (cardId: string, choiceId: string) =>
    request<ChoiceCard>(`/choice-cards/${cardId}/answer`, {
      method: "POST",
      body: JSON.stringify({ choiceId }),
    }),

  dismissChoiceCard: (cardId: string) =>
    request<ChoiceCard>(`/choice-cards/${cardId}/dismiss`, { method: "POST", body: "{}" }),

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

  getNotifications: (limit = 50) =>
    request<NotificationEntry[]>(`/notifications?limit=${limit}`),

  getPromptTemplates: () =>
    request<PromptTemplate[]>("/prompt-templates"),

  dismissTask: (sessionId: string, taskId: string): Promise<void> =>
    request<void>(`/sessions/${sessionId}/tasks/${taskId}/dismiss`, {
      method: "POST",
    }),

  listBackgroundTasks: (sessionId: string) =>
    request<{ tasks: BackgroundTaskDTO[] }>(
      `/sessions/${sessionId}/background-tasks`
    ),

  listAutomations: () => request<Automation[]>("/automations"),

  // Experimental "loops" only: 404 while the feature is off.
  listAutomationSummaries: () =>
    request<AutomationSummary[]>("/automations/summary"),

  getAutomation: (id: string) => request<Automation>(`/automations/${id}`),

  // Experimental "projects" (#805): 404 while the feature is off.
  listProjects: () => request<ProjectSummary[]>("/projects"),
  getProject: (path: string) =>
    request<ProjectDetail>(`/projects/${encodeURIComponent(path)}`),
  setProjectCoordinator: (path: string, sessionId: string) =>
    request<ProjectDetail>(`/projects/${encodeURIComponent(path)}/coordinator`, {
      method: "PUT",
      body: JSON.stringify({ sessionId }),
    }),

  createAutomation: (data: AutomationInput) =>
    request<Automation>("/automations", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  updateAutomation: (id: string, data: AutomationInput) =>
    request<Automation>(`/automations/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  deleteAutomation: (id: string) =>
    request<{ status: string }>(`/automations/${id}`, { method: "DELETE" }),

  setAutomationEnabled: (id: string, enabled: boolean) =>
    request<Automation>(`/automations/${id}/enabled`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),

  listAutomationRuns: (id: string, limit = 50) =>
    request<AutomationRun[]>(`/automations/${id}/runs?limit=${limit}`),

  listAutomationReports: (id: string, limit = 100) =>
    request<AutomationRunReport[]>(`/automations/${id}/reports?limit=${limit}`),

  getAutomationTimeline: (id: string, before?: string, limit = 30) => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (before) q.set("before", before);
    return request<AutomationTimeline>(`/automations/${id}/timeline?${q}`);
  },

  listAutomationTasks: (id: string) =>
    request<AutomationTask[]>(`/automations/${id}/tasks`),

  addAutomationTask: (id: string, title: string, body = "") =>
    request<AutomationTask>(`/automations/${id}/tasks`, {
      method: "POST",
      body: JSON.stringify({ title, body }),
    }),

  updateAutomationTask: (
    id: string,
    taskId: number,
    data: { title?: string; body?: string; status?: AutomationTask["status"] },
  ) =>
    request<AutomationTask>(`/automations/${id}/tasks/${taskId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    }),

  deleteAutomationTask: (id: string, taskId: number) =>
    request<{ status: string }>(`/automations/${id}/tasks/${taskId}`, { method: "DELETE" }),

  listArtifacts: (sessionId: string) =>
    request<Artifact[]>(`/sessions/${sessionId}/artifacts`),
  listAllArtifacts: () => request<Artifact[]>("/artifacts"),
  getArtifact: (id: string) => request<Artifact>(`/artifacts/${id}`),
  deleteArtifact: (id: string) =>
    request<{ status: string }>(`/artifacts/${id}`, { method: "DELETE" }),

  // Secrets: metadata only — the API never returns values.
  listSecrets: () => request<SecretMeta[]>("/secrets"),
  deleteSecret: (name: string) =>
    request<{ status: string }>(`/secrets/${encodeURIComponent(name)}`, { method: "DELETE" }),
  listSecretUsages: (name?: string, limit = 50) => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (name) q.set("name", name);
    return request<SecretUsage[]>(`/secrets/usages?${q}`);
  },

};

export interface AutomationRef {
  id: string;
  name: string;
}

// AutomationSummary mirrors the Go automationSummary (session list loop row).
export interface AutomationSummary {
  id: string;
  name: string;
  enabled: boolean;
  triggerType: "interval" | "cron" | "queue";
  state: "running" | "waiting" | "stopped";
  stoppedReason?: string;
  pendingTasks: number;
  nextFireAt: string | null;
  nextReason?: string;
  lastRunAt: string | null;
  createdAt: string;
  recentRuns: {
    runId: number;
    sessionId: string;
    sessionName: string;
    sessionStatus: string;
    firedAt: string;
  }[];
  feeders: AutomationRef[];
  feeds: AutomationRef[];
}

// Mirrors the Go projectSummary / projectDetail (internal/server/projects.go).
export interface ProjectSummary {
  path: string;
  name: string;
  coordinator: { id: string; name: string; status: string } | null;
  activeSessions: number;
  automations: { running: number; waiting: number; stopped: number };
  pendingEscalations: number;
  lastUpdatedAt: string | null;
}

export interface ProjectAutomation {
  id: string;
  name: string;
  enabled: boolean;
  triggerType: string;
  state: "running" | "waiting" | "stopped";
  lastRunAt: string | null;
  nextFireAt: string | null;
  // loops only
  pendingTasks?: number;
  stoppedReason?: string;
  latestReport?: { metrics: Record<string, number>; summary: string } | null;
}

export interface ProjectDetail extends ProjectSummary {
  sessions: {
    id: string;
    name: string;
    status: string;
    type: string;
    projectPath: string;
    updatedAt: string;
  }[];
  automationList: ProjectAutomation[];
  escalations: { id: string; sessionId: string; message: string }[];
}

export interface Automation {
  // Only present while the experimental "loops" feature is on.
  feeders?: AutomationRef[];
  feeds?: AutomationRef[];
  id: string;
  name: string;
  prompt: string;
  triggerType: "interval" | "cron" | "queue";
  triggerValue: string;
  projectPath?: string;
  model?: string;
  enabled: boolean;
  maxRuns?: number;
  until?: string;
  watchMetric?: string;
  watchDirection?: "higher" | "lower";
  createdAt: string;
  updatedAt: string;
}

export interface AutomationInput {
  name: string;
  prompt: string;
  triggerType: "interval" | "cron" | "queue";
  triggerValue: string;
  projectPath?: string;
  model?: string;
  enabled: boolean;
  // Loop safety valves (experimental "loops"). until is RFC3339; "" clears it.
  maxRuns?: number;
  until?: string;
  // Metric watched for stagnation / dashboard coloring (experimental "loops").
  // watchMetric "" clears it; watchDirection is required when it is set.
  watchMetric?: string;
  watchDirection?: "higher" | "lower" | "";
}

export interface AutomationRun {
  id: number;
  automationId: string;
  firedAt: string;
  sessionId?: string;
  status: "success" | "skipped" | "error";
  message?: string;
  createdAt: string;
}

// AutomationRunReport mirrors `internal/automation.RunReport` (Go). Experimental "loops".
export interface AutomationRunReport extends AutomationRun {
  report: {
    runId: number;
    metrics: Record<string, number>;
    summary: string;
    links: { title: string; url: string }[];
    createdAt: string;
  } | null;
  durationSeconds: number;
  stopped: boolean;
  cost: number;
  inputTokens: number;
  outputTokens: number;
}

// AutomationTimelineEvent mirrors `internal/automation.TimelineEvent` (Go). Experimental "loops".
export interface AutomationTimelineEvent {
  kind: "run" | "task_added" | "task_finished" | "disabled";
  at: string;
  run?: AutomationRunReport;
  runTasks?: AutomationTask[];
  // Set when the run belongs to a loop that fed tasks into this one.
  feeder?: AutomationRef;
  addedTasks?: string[];
  task?: AutomationTask;
  source?: AutomationRef;
  sourceSessionId?: string;
  message?: string;
}

export interface AutomationTimeline {
  events: AutomationTimelineEvent[];
  nextBefore: string;
}

// AutomationTask mirrors `internal/automation.Task` (Go). Experimental "loops".
export interface AutomationTask {
  id: number;
  automationId: string;
  title: string;
  body: string;
  status: "pending" | "in_progress" | "done" | "failed";
  runId?: number;
  result: string;
  createdBy: "user" | "agent";
  sourceAutomationId?: string;
  attempts: number;
  createdAt: string;
  updatedAt: string;
}

// BackgroundTaskDTO mirrors `internal/session.BackgroundTask` (Go).
export interface BackgroundTaskDTO {
  taskId: string;
  taskType: string;
  agentId?: string;
  toolUseId?: string;
  description?: string;
  startedAt?: string;
  lastToolName?: string;
  lastToolInput?: unknown;
  output?: string;
  usage?: { inputTokens?: number; outputTokens?: number };
  toolCallHistory: { toolName: string; description?: string; timestamp?: string }[];
}

export interface RecentProject {
  projectPath: string;
  lastUsedAt: string;
  sessionCount: number;
}

export interface HostMachineInfo {
  os: string;
  arch: string;
  kernelVersion: string;
  hostname: string;
  ipAddresses: string[];
  pid: number;
  uptime: string;
  memoryBytes: number;
}

export interface HostProviderInfo {
  name: string;
  command: string;
  available: boolean;
}

export interface HostInfo {
  machine: HostMachineInfo;
  providers: HostProviderInfo[];
  skills: string[];
}

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

export interface RoleTemplate {
  name: string;
  provider: string;
  model?: string;
  systemPrompt: string;
}

export interface PromptTemplate {
  name: string;
  prompt: string;
  category?: string;
}

export interface AppConfig {
  server: { port: number; frontendDir?: string };
  daemon: {
    interval: string;
    backgroundTaskNotificationInterval?: string;
    backgroundTaskNotificationEnabled?: boolean;
  };
  session: { claudeCommand: string; cursorCommand?: string; defaultRole?: string; defaultModel?: string; claudeDefaultModel?: string; codexDefaultModel?: string; cursorDefaultModel?: string };
  devMode: boolean;
  prompts?: { systemPrompt: string };
  templates: RoleTemplate[];
  promptTemplates?: PromptTemplate[];
  configPath?: string;
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

export interface NotificationEntry {
  id: number;
  sessionId: string;
  sessionName: string;
  kind: string;
  message: string;
  createdAt: string;
}

export interface MetricEvent {
  id: number;
  name: string;
  body: string;
  attributes: Record<string, string>;
  sessionId: string;
  timestamp: string;
}
