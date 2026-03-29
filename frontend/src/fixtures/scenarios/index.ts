import { rateLimitAllowedLines } from "./rateLimitAllowed";
import { rateLimitRejectedLines } from "./rateLimitRejected";
import { escalationLines } from "./escalation";
import { agentSubtaskLines } from "./agentSubtask";
import { normalConversationLines } from "./normalConversation";
import { compactionLines } from "./compaction";
import { multipleRunningTasksLines } from "./multipleRunningTasks";
import { apiRetryLines, apiRetryResolvedLines } from "./apiRetry";
import { mockSessions } from "./sessionListScenarios";
import type { Session } from "../../types/session";

export interface SimulatedPermission {
  id: string;
  toolName: string;
  input: unknown;
  timedOut?: boolean;
  timeoutSeconds?: number;
}

export interface SimulatedEscalation {
  id: string;
  message: string;
  timedOut?: boolean;
  timeoutSeconds?: number;
}

export type ScenarioPresetType = "stream" | "session-list";

export interface ScenarioPreset {
  id: string;
  type: ScenarioPresetType;
  label: string;
  description: string;
  lines?: unknown[];
  mockSessions?: Session[];
  simulatedPermission?: SimulatedPermission;
  simulatedEscalation?: SimulatedEscalation;
  simulateSendError?: boolean;
}

export const scenarioPresets: ScenarioPreset[] = [
  {
    id: "rate-limit-allowed",
    type: "stream",
    label: "Rate Limit (Allowed Warning)",
    description: "seven_day utilization 90% の allowed_warning",
    lines: rateLimitAllowedLines,
  },
  {
    id: "rate-limit-rejected",
    type: "stream",
    label: "Rate Limit (Rejected)",
    description: "five_hour の rejected + allowed_warning",
    lines: rateLimitRejectedLines,
  },
  {
    id: "escalation",
    type: "stream",
    label: "Escalation",
    description: "escalateツールでユーザーに質問（バナー付き）",
    lines: escalationLines,
    simulatedEscalation: {
      id: "esc-001",
      message: "プレビュー環境の動作確認が完了しました。\n\n- セッション一覧: 正常表示\n- セッション詳細: ストリーム出力正常\n- エスカレーション: 応答可能\n\nこのままマージしてよいですか？",
      timeoutSeconds: 300,
    },
  },
  {
    id: "escalation-timed-out",
    type: "stream",
    label: "Escalation: Timed Out",
    description: "エスカレーションがタイムアウトした状態",
    lines: escalationLines,
    simulatedEscalation: {
      id: "esc-002",
      message: "設計方針について確認したいことがあります。Fork機能の実装で、ファイルシステムは共有でよいですか？",
      timedOut: true,
      timeoutSeconds: 60,
    },
  },
  {
    id: "agent-subtask",
    type: "stream",
    label: "Agent Subtask",
    description: "Agentツール呼び出しとtask_progress進行",
    lines: agentSubtaskLines,
  },
  {
    id: "compaction",
    type: "stream",
    label: "Compaction",
    description: "コンテキストウィンドウ圧縮(compact_boundary)前後の会話",
    lines: compactionLines,
  },
  {
    id: "normal-conversation",
    type: "stream",
    label: "Normal Conversation",
    description: "ツール呼び出しを含む通常の調査会話",
    lines: normalConversationLines,
  },
  {
    id: "permission-exit-plan",
    type: "stream",
    label: "Permission: Exit Plan Mode",
    description: "プランモード終了時のpermission_promptバナー",
    lines: normalConversationLines,
    simulatedPermission: {
      id: "perm-001",
      toolName: "ExitPlanMode",
      input: {
        plan: "## 実装計画\n\n1. DBスキーマにparent_session_idカラムを追加\n2. ForkSession()メソッドをManagerに実装\n3. APIエンドポイント POST /api/sessions/{id}/fork を追加\n4. ストリーム履歴(.jsonl)のコピー処理\n5. フロントエンドにForkボタンを追加\n6. CLIに agmux session fork <id> コマンドを追加",
      },
      timeoutSeconds: 300,
    },
  },
  {
    id: "permission-timed-out",
    type: "stream",
    label: "Permission: Timed Out",
    description: "permission_promptがタイムアウトした状態",
    lines: normalConversationLines,
    simulatedPermission: {
      id: "perm-002",
      toolName: "Bash",
      input: { command: "rm -rf /tmp/old-cache" },
      timedOut: true,
      timeoutSeconds: 30,
    },
  },
  {
    id: "api-retry",
    type: "stream",
    label: "API Retry (リトライ中)",
    description: "最後のイベントがリトライ — 末尾にインジケーター表示",
    lines: apiRetryLines,
  },
  {
    id: "api-retry-resolved",
    type: "stream",
    label: "API Retry (解消済み)",
    description: "リトライ後にassistantが再開 — インジケーター非表示",
    lines: apiRetryResolvedLines,
  },
  {
    id: "multiple-running-tasks",
    type: "stream",
    label: "Multiple Running Tasks",
    description: "7つの並列タスクが同時実行（ActiveTasksPanel collapse検証用）",
    lines: multipleRunningTasksLines,
  },
  {
    id: "send-error",
    type: "stream",
    label: "Send Error Simulation",
    description: "送信エラー時にメッセージが保持されることを確認",
    lines: normalConversationLines,
    simulateSendError: true,
  },
  {
    id: "session-list-all-states",
    type: "session-list",
    label: "Session List: All States",
    description: "各種ステータス・サブセッション・ロール等を含む一覧表示",
    mockSessions: mockSessions,
  },
];
