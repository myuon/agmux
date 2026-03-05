export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  tmuxSession: string;
  status: "working" | "idle" | "question_waiting" | "stopped";
  type: "worker" | "controller";
  outputMode: "terminal" | "stream";
  currentTask?: string;
  goal?: string;
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
