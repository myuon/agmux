export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  status: "working" | "idle" | "paused" | "exited" | "waiting_input" | "archived";
  type: "worker" | "controller" | "external" | "ephemeral";
  provider: string;
  model?: string;
  parentSessionId?: string;
  roleTemplate?: string;
  currentTask?: string;
  goal?: string;
  goals?: { currentTask: string; goal: string }[];
  lastError?: string;
  conversationStarted?: boolean;
  ephemeralTimeoutSeconds?: number;
  completionReport?: string;
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
