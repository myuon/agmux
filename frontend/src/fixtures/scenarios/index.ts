import { rateLimitAllowedLines } from "./rateLimitAllowed";
import { rateLimitRejectedLines } from "./rateLimitRejected";
import { escalationLines } from "./escalation";
import { permissionPromptLines } from "./permissionPrompt";
import { agentSubtaskLines } from "./agentSubtask";
import { normalConversationLines } from "./normalConversation";

export interface ScenarioPreset {
  id: string;
  label: string;
  description: string;
  lines: unknown[];
}

export const scenarioPresets: ScenarioPreset[] = [
  {
    id: "rate-limit-allowed",
    label: "Rate Limit (Allowed Warning)",
    description: "five_hour / overageStatus rejected の利用率警告",
    lines: rateLimitAllowedLines,
  },
  {
    id: "rate-limit-rejected",
    label: "Rate Limit (Rejected)",
    description: "seven_day レート制限によるリクエスト拒否",
    lines: rateLimitRejectedLines,
  },
  {
    id: "escalation",
    label: "Escalation",
    description: "escalateツール呼び出し中の表示",
    lines: escalationLines,
  },
  {
    id: "permission-prompt",
    label: "Permission Prompt",
    description: "permission_prompt発生中の表示",
    lines: permissionPromptLines,
  },
  {
    id: "agent-subtask",
    label: "Agent Subtask",
    description: "子タスク (task_progress) 進行中の表示",
    lines: agentSubtaskLines,
  },
  {
    id: "normal-conversation",
    label: "Normal Conversation",
    description: "テキスト + ツール呼び出しの基本フロー",
    lines: normalConversationLines,
  },
];
