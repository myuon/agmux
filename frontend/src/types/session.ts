export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  status: "working" | "idle" | "paused" | "question_waiting" | "alignment_needed" | "stopped";
  type: "worker" | "controller" | "external";
  provider: string;
  model?: string;
  currentTask?: string;
  goal?: string;
  goals?: { currentTask: string; goal: string }[];
  lastError?: string;
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
