export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  tmuxSession: string;
  status: "running" | "waiting" | "error" | "done" | "stopped";
  createdAt: string;
  updatedAt: string;
}
