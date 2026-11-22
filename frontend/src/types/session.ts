export interface Session {
  id: string;
  name: string;
  /** How name was set: "manual" | "default" (awaiting auto-title) | "generating" | "auto" */
  nameSource?: string;
  projectPath: string;
  initialPrompt?: string;
  status: "working" | "idle" | "paused" | "exited" | "waiting_input" | "archived";
  type: "worker" | "controller" | "external" | "ephemeral";
  provider: string;
  model?: string;
  /** Model switch reserved during a running turn; applied from the next turn. */
  pendingModel?: string;
  parentSessionId?: string;
  roleTemplate?: string;
  automationId?: string;
  currentTask?: string;
  goal?: string;
  goals?: { currentTask: string; goal: string }[];
  lastError?: string;
  conversationStarted?: boolean;
  ephemeralTimeoutSeconds?: number;
  completionReport?: string;
  /** A finished turn the user has not seen yet (#859). */
  unread?: boolean;
  createdAt: string;
  updatedAt: string;
  githubUrl?: string;
  branch?: string;
  pullRequests?: PullRequest[];
}

export interface PullRequest {
  number: number;
  title: string;
  url: string;
  state: string;
}
