export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  tmuxSession: string;
  status: "working" | "idle" | "paused" | "question_waiting" | "alignment_needed" | "stopped";
  type: "worker" | "controller";
  provider: string;
  model?: string;
  outputMode: "terminal" | "stream";
  currentTask?: string;
  goal?: string;
  goals?: { currentTask: string; goal: string }[];
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
