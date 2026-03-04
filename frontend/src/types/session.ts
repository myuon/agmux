export interface Session {
  id: string;
  name: string;
  projectPath: string;
  initialPrompt?: string;
  tmuxSession: string;
  status: "working" | "idle" | "question_waiting" | "stopped";
  type: "worker" | "controller";
  createdAt: string;
  updatedAt: string;
}
