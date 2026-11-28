// --- Stream mode types and helpers ---

export interface StreamEntry {
  type: string;
  parent_tool_use_id?: string | null;
  message?: {
    role?: string;
    content?: unknown;
  };
}

export interface StreamContentBlock {
  type: string;
  id?: string;
  tool_use_id?: string;
  text?: string;
  name?: string;
  input?: unknown;
  content?: unknown;
  thinking?: string;
  is_error?: boolean;
  source?: { type: string; media_type: string; data: string };
}

// stream_event from --include-partial-messages (real-time deltas via WebSocket only)
export interface StreamEvent {
  type: "stream_event";
  event: {
    type: string;
    index?: number;
    delta?: {
      type: string;
      text?: string;
    };
  };
}

// AskUserQuestion input types
export interface AskUserQuestionOption {
  label: string;
  description?: string;
}

export interface AskUserQuestionItem {
  question: string;
  header?: string;
  options: AskUserQuestionOption[];
  multiSelect?: boolean;
}

// A display item for the merged stream view
export type StreamDisplayItem =
  | { kind: "text"; text: string }
  | { kind: "image"; mediaType: string; data: string }
  | { kind: "tool_call"; name: string; input: unknown; result?: string; resultImages?: Array<{ mediaType: string; data: string }>; toolUseId?: string; running?: boolean; isError?: boolean; backgroundTask?: BackgroundTaskInfo }
  | { kind: "thinking"; text: string }
  | { kind: "system_event"; eventType: string; label: string; detail?: string }
  | { kind: "rate_limit"; rateLimitType: string; status: string; resetsAt: number; utilization?: number; isUsingOverage?: boolean; overageStatus?: string }
  | { kind: "choice_card"; cardId: string }
  | { kind: "api_retry"; attempt: number; maxRetries: number; retryDelayMs: number; errorStatus?: number; error?: string }
  | { kind: "result"; isError: boolean; result: string; subtype: string; numTurns?: number; durationMs?: number; costUsd?: number; terminalReason?: string; errors?: string[] };

// State of a background task (run_in_background Bash, a Bash moved to the
// background on timeout, or a backgrounded Agent) attached to the tool call
// that launched it. status is "running" until a task_updated/task_notification
// reports a terminal status (completed / failed / killed / stopped / ...).
export interface BackgroundTaskInfo {
  taskId?: string;
  status: string;
  taskType?: string;
  description?: string;
  summary?: string;
  outputFile?: string;
}

// Active task tracking for sub-agents and background tasks
export interface ActiveTask {
  taskId: string;
  taskType: string; // "local_agent" | "local_bash"
  agentId?: string;
  // tool_use id of the Agent/Task call that spawned this task (from task_started).
  // Used to open the subagent thread (lines with parent_tool_use_id = toolUseId).
  toolUseId?: string;
  description?: string;
  startedAt?: string;
  lastToolName?: string;
  lastToolInput?: unknown;
  output?: string;
  usage?: { inputTokens?: number; outputTokens?: number };
}

export function extractActiveTasks(entries: StreamEntry[]): ActiveTask[] {
  // Pass 1: collect tool_use inputs by id from assistant messages
  // Also collect task_ids stopped via TaskStop
  const toolUseInputs = new Map<string, { name: string; input: unknown }>();
  const stoppedTaskIds = new Set<string>();
  // Tasks started by a subagent (e.g. a Bash it ran) are not the main agent's
  // tasks and must not be listed. CLI flags only task_started; the server
  // annotates later task_* lines of such tasks too.
  const subagentOwnedTaskIds = new Set<string>();
  for (const entry of entries) {
    if (entry.type === "system") {
      const raw = entry as unknown as Record<string, unknown>;
      if (raw.owned_by_subagent === true && typeof raw.task_id === "string") {
        subagentOwnedTaskIds.add(raw.task_id);
      }
      continue;
    }
    if (entry.type !== "assistant") continue;
    const content = entry.message?.content;
    if (!Array.isArray(content)) continue;
    for (const block of content) {
      const b = block as Record<string, unknown>;
      if (b.type === "tool_use" && typeof b.id === "string" && b.input != null) {
        toolUseInputs.set(b.id, { name: b.name as string, input: b.input });
      }
      if (b.type === "tool_use" && b.name === "TaskStop" && b.input) {
        const input = b.input as Record<string, unknown>;
        if (typeof input.task_id === "string") {
          stoppedTaskIds.add(input.task_id);
        }
      }
    }
  }

  // Pass 2: process task events
  const tasks = new Map<string, ActiveTask>();
  for (const entry of entries) {
    const raw = entry as unknown as Record<string, unknown>;
    if (entry.type !== "system") continue;
    if (typeof raw.task_id === "string" && subagentOwnedTaskIds.has(raw.task_id)) continue;
    const subtype = raw.subtype as string | undefined;
    if (subtype === "task_started") {
      const taskId = raw.task_id as string;
      const taskType = raw.task_type as string;
      const toolUseId = raw.tool_use_id as string | undefined;
      if (taskId) {
        const task: ActiveTask = { taskId, taskType: taskType || "unknown" };
        if (raw.agent_id) task.agentId = raw.agent_id as string;
        if (toolUseId) task.toolUseId = toolUseId;
        if (raw.description) task.description = raw.description as string;
        if (raw.timestamp) task.startedAt = raw.timestamp as string;
        // Resolve tool input from the corresponding tool_use block
        if (toolUseId && toolUseInputs.has(toolUseId)) {
          const tu = toolUseInputs.get(toolUseId)!;
          task.lastToolName = tu.name;
          task.lastToolInput = tu.input;
        }
        tasks.set(taskId, task);
      }
    } else if (subtype === "task_progress") {
      const taskId = raw.task_id as string;
      if (taskId) {
        if (!tasks.has(taskId)) {
          const taskType = (raw.task_type as string) || "unknown";
          const task: ActiveTask = { taskId, taskType };
          if (raw.agent_id) task.agentId = raw.agent_id as string;
          tasks.set(taskId, task);
        }
        const task = tasks.get(taskId)!;
        if (raw.description) task.description = raw.description as string;
        if (raw.last_tool_name) task.lastToolName = raw.last_tool_name as string;
        if (raw.last_tool_input !== undefined) task.lastToolInput = raw.last_tool_input;
        if (raw.output) task.output = raw.output as string;
        if (raw.usage) {
          const usage = raw.usage as Record<string, unknown>;
          task.usage = {
            inputTokens: usage.input_tokens as number | undefined,
            outputTokens: usage.output_tokens as number | undefined,
          };
        }
      }
    } else if (subtype === "task_notification") {
      const taskId = raw.task_id as string;
      if (taskId) {
        tasks.delete(taskId);
      }
    } else if (subtype === "task_updated") {
      const taskId = raw.task_id as string;
      const patch = raw.patch as Record<string, unknown> | undefined;
      const patchStatus = patch?.status as string | undefined;
      if (taskId && (patchStatus === "completed" || patchStatus === "failed")) {
        tasks.delete(taskId);
      }
    }
  }
  // Remove tasks that were explicitly stopped via TaskStop
  for (const id of stoppedTaskIds) {
    tasks.delete(id);
  }
  return Array.from(tasks.values());
}

export function parseStreamContentBlocks(entry: StreamEntry): StreamContentBlock[] {
  // Check both entry.message.content and top-level entry.content (some entries
  // have content at the top level without a message wrapper)
  const raw = entry as unknown as Record<string, unknown>;
  const content = entry.message?.content ?? raw.content;
  if (!content) return [];
  if (typeof content === "string") {
    return content ? [{ type: "text", text: content }] : [];
  }
  if (!Array.isArray(content)) return [];
  return content;
}

// Parse a system event entry into a display item, or return null if not displayable
/**
 * System subtypes that are intentionally not shown in the stream. They are
 * internal state notifications from the CLI with nothing for the user to act
 * on. Listing them here (instead of silently falling through) records the
 * decision: the session-monitoring scanner treats subtypes in this set as
 * handled, not as unsupported.
 */
export const HIDDEN_SYSTEM_SUBTYPES = new Set([
  "background_tasks_changed",
  "vcs_state_changed",
  "commands_changed",
  "code_change_published",
  "thinking_tokens",
  // フック実行中の途中出力。成否は hook_response で出す（失敗時のみ表示）ので、途中経過は見せない
  "hook_progress",
  "hook_started",
  // CLI のテレメトリ的な通知（例: iOS 開発コマンドの検出 kind=ios_app）。ユーザーが見る情報はない
  "dev_intent",
]);

// Labels a user text block the CLI marked isSynthetic.
export function parseSyntheticUserText(text: string): StreamDisplayItem {
  const trimmed = text.trim();
  if (trimmed.startsWith("This session is being continued from a previous conversation")) {
    return { kind: "system_event", eventType: "compact_summary", label: "コンパクション後の要約", detail: trimmed };
  }
  const stopHook = trimmed.match(/^Stop hook feedback:\s*([\s\S]*)$/);
  if (stopHook) {
    return { kind: "system_event", eventType: "stop_hook_feedback", label: "Stop フックのフィードバック", detail: stopHook[1].trim() || undefined };
  }
  if (/^\[Image: original \d+x\d+/.test(trimmed)) {
    return { kind: "system_event", eventType: "image_scale_note", label: "画像の縮尺注記", detail: trimmed };
  }
  return { kind: "system_event", eventType: "synthetic_user_text", label: "システムが追加したメッセージ", detail: trimmed };
}

function parseSystemEvent(entry: StreamEntry): StreamDisplayItem | null {
  const raw = entry as unknown as Record<string, unknown>;
  const subtype = raw.subtype as string | undefined;

  if (subtype && HIDDEN_SYSTEM_SUBTYPES.has(subtype)) {
    return null;
  }

  if (subtype === "compact_boundary") {
    const meta = raw.compact_metadata as { trigger?: string; pre_tokens?: number } | undefined;
    const tokens = meta?.pre_tokens ? `${Math.round(meta.pre_tokens / 1000)}k tokens` : "";
    return {
      kind: "system_event",
      eventType: "compact",
      label: "コンテキストをコンパクション",
      detail: tokens ? `(${tokens})` : undefined,
    };
  }

  if (subtype === "status") {
    const status = raw.status as string | null;
    if (status === "compacting") {
      return {
        kind: "system_event",
        eventType: "compacting",
        label: "コンパクション中...",
      };
    }
    // status: null means compaction finished — skip (compact_boundary covers it)
    return null;
  }

  if (subtype === "task_started") {
    return null;
  }

  if (subtype === "init") {
    return null;
  }

  if (subtype === "api_retry") {
    // api_retry is not rendered inline; shown as a trailing indicator in StreamOutputView
    return null;
  }

  // The holder replaces a stdout line that is too large to keep (or an input
  // line too large to deliver) by this event; `message` holds the notice.
  if (subtype === "oversized_output" || subtype === "oversized_input") {
    const message = typeof raw.message === "string" && raw.message ? raw.message : "出力が大きすぎるため省略しました";
    return {
      kind: "system_event",
      eventType: subtype,
      label: message,
    };
  }

  // CLI notifications (e.g. key=stop-hook-error). The text may end with a
  // TUI-only key hint such as " · ctrl+o to see", which is meaningless here.
  if (subtype === "notification") {
    const text = typeof raw.text === "string" ? raw.text.replace(/\s*·\s*ctrl\+\S+ to \S+$/, "").trim() : "";
    if (!text) return null;
    return {
      kind: "system_event",
      eventType: "notification",
      label: text,
    };
  }

  // Hook lifecycle events. Successful runs are noise; only a failed hook is
  // surfaced so the user can notice it (e.g. a SessionStart hook crashing).
  if (subtype === "hook_started") {
    return null;
  }
  if (subtype === "hook_response") {
    if (raw.outcome !== "error") return null;
    const hookName = typeof raw.hook_name === "string" && raw.hook_name ? raw.hook_name : "不明";
    const exitCode = typeof raw.exit_code === "number" ? ` (exit ${raw.exit_code})` : "";
    const pick = (v: unknown) => (typeof v === "string" ? v.trim() : "");
    const output = pick(raw.stderr) || pick(raw.output) || pick(raw.stdout);
    return {
      kind: "system_event",
      eventType: "hook_error",
      label: `⚠ フック ${hookName} が失敗しました${exitCode}`,
      detail: output || undefined,
    };
  }
  // CLI informational messages (e.g. tag=hook_no_verdict when a PreToolUse
  // hook timed out). `content` is the human-readable text; `level` is
  // info/warning/error.
  if (subtype === "informational") {
    const content = typeof raw.content === "string" ? raw.content.trim() : "";
    if (!content) return null;
    const level = typeof raw.level === "string" ? raw.level : "";
    return {
      kind: "system_event",
      eventType: "informational",
      label: level === "warning" || level === "error" ? `⚠ ${content}` : content,
    };
  }

  // The CLI could not persist some session data (e.g. the transcript) to disk,
  // typically because the disk is full (error_code=ENOSPC). Surface it as a
  // warning so the user knows the session may not be resumable.
  if (subtype === "session_writes_lost") {
    const errorCode = typeof raw.error_code === "string" && raw.error_code ? raw.error_code : "";
    const lost = Array.isArray(raw.lost) ? raw.lost.filter((v): v is string => typeof v === "string") : [];
    const what = lost.length > 0 ? lost.join(", ") : "セッションのデータ";
    return {
      kind: "system_event",
      eventType: "session_writes_lost",
      label: `⚠ ${what} を保存できませんでした${errorCode ? ` (${errorCode})` : ""}`,
      detail: errorCode === "ENOSPC" ? "ディスクの空き容量が不足しています" : undefined,
    };
  }

  // The model refused the user's message and no fallback model was available
  // (e.g. api_refusal_category=reasoning_extraction). Surface it as a warning
  // with the API's explanation so the user knows why there was no reply.
  if (subtype === "model_refusal_no_fallback") {
    const explanation =
      typeof raw.api_refusal_explanation === "string" ? raw.api_refusal_explanation.trim() : "";
    const model = typeof raw.original_model === "string" && raw.original_model ? ` (${raw.original_model})` : "";
    return {
      kind: "system_event",
      eventType: "model_refusal_no_fallback",
      label: `⚠ メッセージがモデルに拒否されました${model}`,
      detail: explanation || undefined,
    };
  }

  if (subtype === "task_notification") {
    const status = typeof raw.status === "string" ? raw.status : "";
    const summary = (raw.summary as string) || "";
    return {
      kind: "system_event",
      eventType: "task_notification",
      label: taskNotificationLabel(status),
      detail: summary || undefined,
    };
  }

  return null;
}

// Japanese label for a standalone task_notification row.
export function taskNotificationLabel(status: string): string {
  switch (status) {
    case "completed":
      return "タスク完了";
    case "failed":
      return "タスク失敗";
    case "killed":
    case "stopped":
      return "タスク停止";
    case "dismissed":
      return "タスク取り消し";
    default:
      return status ? `タスク ${status}` : "タスク通知";
  }
}

// --- Task (task_started / task_updated / task_notification) folding ---

interface TaskInfo {
  taskId?: string;
  isBackgrounded: boolean;
  status: string;
  taskType?: string;
  description?: string;
  summary?: string;
  outputFile?: string;
}

// Map of tool_use_id -> task info, built from all task_* lines in `entries`
// (the notification may arrive long after the tool call). task_updated carries
// only task_id, so it is resolved via the task_started line (taskToToolUse).
interface TaskInfoMap {
  byToolUse: Map<string, TaskInfo>;
  taskToToolUse: Map<string, string>;
}

// result.origin of kind "peer": a message sent from another session.
function parsePeerOrigin(origin: unknown): StreamDisplayItem | null {
  if (!origin || typeof origin !== "object") return null;
  const o = origin as Record<string, unknown>;
  if (o.kind !== "peer") return null;
  const body = typeof o.body === "string" ? o.body.trim() : "";
  if (!body) return null;
  const name = typeof o.name === "string" && o.name ? o.name : "";
  return {
    kind: "system_event",
    eventType: "peer_message",
    label: name ? `他のセッション（${name}）からのメッセージ` : "他のセッションからのメッセージ",
    detail: body,
  };
}

function collectTaskInfo(entries: StreamEntry[]): TaskInfoMap {
  const byToolUse = new Map<string, TaskInfo>();
  const taskToToolUse = new Map<string, string>();
  const map: TaskInfoMap = { byToolUse, taskToToolUse };
  for (const entry of entries) {
    if (entry.type !== "system") continue;
    const raw = entry as unknown as Record<string, unknown>;
    const subtype = raw.subtype;
    if (subtype !== "task_started" && subtype !== "task_updated" && subtype !== "task_notification") continue;
    const taskId = typeof raw.task_id === "string" ? raw.task_id : undefined;
    if (subtype === "task_started" && taskId && typeof raw.tool_use_id === "string") {
      taskToToolUse.set(taskId, raw.tool_use_id);
    }
    const toolUseId = resolveTaskToolUseId(raw, map);
    if (!toolUseId) continue;
    const info: TaskInfo = byToolUse.get(toolUseId) ?? { taskId, isBackgrounded: false, status: "running" };
    if (!info.taskId) info.taskId = taskId;
    if (subtype === "task_started") {
      if (raw.is_backgrounded === true) info.isBackgrounded = true;
      if (typeof raw.task_type === "string") info.taskType = raw.task_type;
      if (typeof raw.description === "string") info.description = raw.description;
    } else if (subtype === "task_updated") {
      const patch = raw.patch as Record<string, unknown> | undefined;
      if (patch?.is_backgrounded === true) info.isBackgrounded = true;
      if (typeof patch?.status === "string") info.status = patch.status;
    } else {
      if (typeof raw.status === "string") info.status = raw.status;
      if (typeof raw.summary === "string" && raw.summary) info.summary = raw.summary;
      if (typeof raw.output_file === "string" && raw.output_file) info.outputFile = raw.output_file;
    }
    byToolUse.set(toolUseId, info);
  }
  return map;
}

function resolveTaskToolUseId(raw: Record<string, unknown>, map: TaskInfoMap): string | undefined {
  if (typeof raw.tool_use_id === "string" && raw.tool_use_id) return raw.tool_use_id;
  if (typeof raw.task_id === "string") return map.taskToToolUse.get(raw.task_id);
  return undefined;
}

// Bash tool_result text when the command runs (or was moved) in the background.
function isBackgroundLaunchResult(result: string): boolean {
  return /^Command running in background with ID:/.test(result) || /was moved to the background/.test(result);
}

function extractBackgroundTaskId(result: string): string | undefined {
  return result.match(/background with ID: ([\w-]+)/)?.[1] ?? result.match(/\(ID: ([\w-]+)\)/)?.[1];
}

function extractBackgroundOutputFile(result: string): string | undefined {
  return result.match(/Output is being written to: (\S+?)\.?(?:\s|$)/)?.[1];
}

export type DisplayGroup =
  | { role: "user" | "assistant"; items: StreamDisplayItem[] }
  | { role: "system"; items: StreamDisplayItem[] };

// Providers that emit tool_use and tool_result co-located in the same assistant
// message content array (instead of the Claude pattern where tool_result lives
// in a follow-up user entry, looked up by tool_use_id).
function isProviderCoLocatedTools(provider?: string): boolean {
  return provider === "codex" || provider === "cursor";
}

// Merge assistant/user entries into display items, pairing tool_use with tool_result by id
// partialText: incremental text from stream_event deltas (shown as "typing" in the last assistant group)
// Parse the `<bash-input>...</bash-input><bash-stdout>...</bash-stdout><bash-stderr>...</bash-stderr><bash-error>...</bash-error>`
// text produced by Command mode exec results, and turn it into a Bash tool_call display item.
// Returns null if the text does not look like a bash exec result.
function parseBashExecText(text: string): StreamDisplayItem | null {
  const inputMatch = text.match(/^<bash-input>([\s\S]*?)<\/bash-input>/);
  if (!inputMatch) return null;
  const command = inputMatch[1];
  const stdoutMatch = text.match(/<bash-stdout>([\s\S]*?)<\/bash-stdout>/);
  const stderrMatch = text.match(/<bash-stderr>([\s\S]*?)<\/bash-stderr>/);
  const errorMatch = text.match(/<bash-error>([\s\S]*?)<\/bash-error>/);
  const resultParts: string[] = [];
  if (stdoutMatch) resultParts.push(stdoutMatch[1]);
  if (stderrMatch) resultParts.push(stderrMatch[1]);
  if (errorMatch) resultParts.push(errorMatch[1]);
  return {
    kind: "tool_call",
    name: "Bash",
    input: { command },
    result: resultParts.length > 0 ? resultParts.join("\n") : undefined,
  };
}

// Tool names that spawn a subagent (Claude CLI renamed "Task" to "Agent").
export function isAgentToolName(name?: string): name is string {
  return name === "Agent" || name === "Task";
}

// Client-side equivalent of the backend FilterSubagentLines
// (internal/session/subagent_stream.go): returns lines belonging to the
// subagent thread spawned by parentToolUseId, including nested subagents.
export function filterSubagentLines(lines: unknown[], parentToolUseId: string): unknown[] {
  const ids = new Set<string>([parentToolUseId]);
  const result: unknown[] = [];
  for (const line of lines) {
    const entry = line as StreamEntry;
    if (!entry || !entry.parent_tool_use_id || !ids.has(entry.parent_tool_use_id)) continue;
    result.push(line);
    if (entry.type !== "assistant") continue;
    for (const b of parseStreamContentBlocks(entry)) {
      if (b.type === "tool_use" && b.id && isAgentToolName(b.name)) ids.add(b.id);
    }
  }
  return result;
}

// rootParentToolUseId selects which thread is rendered at the top level:
// - null (default): the main agent thread. Every entry with a non-null
//   parent_tool_use_id belongs to some subagent and is excluded, regardless of
//   whether the parent Agent tool_use is part of `entries`.
// - "<tool_use_id>": the subagent thread spawned by that Agent tool_use.
//   Entries of nested subagents are excluded the same way.
// Finds the user text entries the CLI injects right after a tool's result
// (Skill content, ToolSearch's "Tool loaded."). The tool_result is located by
// tool_use_id rather than position, since parallel tool calls interleave other
// tool_use/tool_result entries and non-user entries (rate_limit_event, system,
// tool_progress) or other threads' lines. Returns the first text-only user entry
// after the result, followed by any consecutive synthetic text entries (e.g.
// skill re-invocation notice + skill content).
function findInjectedUserTextIndices(entries: StreamEntry[], toolUseIdx: number, toolUseId: string): number[] {
  const thread = entries[toolUseIdx].parent_tool_use_id ?? null;
  const messageId = (entries[toolUseIdx].message as { id?: unknown } | undefined)?.id;
  const limit = Math.min(entries.length, toolUseIdx + 200);
  const sameThreadUser = (e: StreamEntry) => e.type === "user" && (e.parent_tool_use_id ?? null) === thread;
  let r = -1;
  for (let j = toolUseIdx + 1; j < limit; j++) {
    const e = entries[j];
    if (!sameThreadUser(e)) continue;
    if (parseStreamContentBlocks(e).some(nb => nb.type === "tool_result" && nb.tool_use_id === toolUseId)) {
      r = j;
      break;
    }
  }
  if (r < 0) return [];
  const found: number[] = [];
  for (let j = r + 1; j < limit; j++) {
    const e = entries[j];
    if ((e.parent_tool_use_id ?? null) !== thread) continue;
    if (e.type !== "user") {
      // The next assistant turn means no injected text follows. Assistant
      // entries of the same message are sibling parallel tool_use blocks.
      if (e.type === "assistant") {
        const id = (e.message as { id?: unknown } | undefined)?.id;
        if (messageId === undefined || id !== messageId) break;
      }
      if (e.type === "result") break;
      continue;
    }
    const blocks = parseStreamContentBlocks(e);
    if (blocks.some(nb => nb.type === "tool_result")) {
      if (found.length > 0) break;
      continue;
    }
    if (!blocks.some(nb => nb.type === "text" && nb.text)) break;
    if (found.length > 0 && (e as { isSynthetic?: unknown }).isSynthetic !== true) break;
    found.push(j);
    if ((e as { isSynthetic?: unknown }).isSynthetic !== true) break;
  }
  return found;
}

export function mergeStreamEntries(entries: StreamEntry[], partialText?: string, provider?: string, rootParentToolUseId: string | null = null): DisplayGroup[] {
  // First pass: collect all tool_results keyed by tool_use_id, and track Skill tool IDs
  const resultMap = new Map<string, string>();
  const resultImageMap = new Map<string, Array<{ mediaType: string; data: string }>>();
  const errorResultIds = new Set<string>();
  const skillToolIds = new Set<string>();
  const toolSearchToolIds = new Set<string>();
  const askUserToolIds = new Set<string>();
  const agentToolIds = new Set<string>();
  // All tool_use ids in the loaded lines (any thread). task_* events whose
  // tool_use_id is among them are folded into that tool call instead of being
  // rendered as standalone rows.
  const allToolUseIds = new Set<string>();
  const taskInfoMap = collectTaskInfo(entries);
  for (const entry of entries) {
    if (entry.type === "assistant") {
      for (const block of parseStreamContentBlocks(entry)) {
        if (block.type === "tool_use" && block.id) allToolUseIds.add(block.id);
        if (block.type === "tool_use" && block.name === "Skill" && block.id) {
          skillToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "ToolSearch" && block.id) {
          toolSearchToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "AskUserQuestion" && block.id) {
          askUserToolIds.add(block.id);
        }
        if (block.type === "tool_use" && isAgentToolName(block.name) && block.id) {
          agentToolIds.add(block.id);
        }
      }
    }
    if (entry.type !== "user") continue;
    for (const block of parseStreamContentBlocks(entry)) {
      // Skip AskUserQuestion tool_results (auto-answered by CLI)
      if (block.type === "tool_result" && block.tool_use_id && askUserToolIds.has(block.tool_use_id)) {
        continue;
      }
      if (block.type === "tool_result" && block.tool_use_id) {
        if (block.is_error === true) errorResultIds.add(block.tool_use_id);
        if (Array.isArray(block.content)) {
          const texts: string[] = [];
          const images: Array<{ mediaType: string; data: string }> = [];
          for (const b of block.content as Array<{ type?: string; text?: string; tool_name?: string; source?: { media_type?: string; data: string } }>) {
            if (b.type === "image" && b.source) {
              images.push({ mediaType: b.source.media_type ?? "", data: b.source.data });
            } else if (b.type === "tool_reference" && b.tool_name) {
              // ToolSearch results: one block per loaded tool, no text.
              texts.push((texts.length > 0 ? "\n" : "") + b.tool_name);
            } else if (b.type === "document") {
              // e.g. PDF read by the Read tool. The body is base64; show a placeholder.
              const mediaType = b.source?.media_type ?? "unknown";
              texts.push((texts.length > 0 ? "\n" : "") + `[document: ${mediaType}]`);
            } else if (b.text) {
              texts.push(b.text);
            }
          }
          resultMap.set(block.tool_use_id, texts.join(""));
          if (images.length > 0) {
            resultImageMap.set(block.tool_use_id, images);
          }
        } else {
          const c = typeof block.content === "string"
            ? block.content
            : JSON.stringify(block.content);
          resultMap.set(block.tool_use_id, c);
        }
      }
    }
  }

  // Helper: whether a tool_use block is still awaiting its tool_result.
  // AskUserQuestion tool_results are intentionally skipped elsewhere, so exclude
  // those ids to avoid marking them as permanently running.
  function isRunning(id?: string): true | undefined {
    return id && !resultMap.has(id) && !askUserToolIds.has(id) ? true : undefined;
  }

  // Background task info for a tool call, or undefined for synchronous calls
  // (whose task_* events are simply hidden).
  function backgroundTaskFor(id?: string): BackgroundTaskInfo | undefined {
    if (!id) return undefined;
    const info = taskInfoMap.byToolUse.get(id);
    const result = resultMap.get(id);
    const backgrounded = info?.isBackgrounded || (result !== undefined && isBackgroundLaunchResult(result));
    if (!backgrounded) return undefined;
    return {
      taskId: info?.taskId ?? (result ? extractBackgroundTaskId(result) : undefined),
      status: info?.status ?? "running",
      taskType: info?.taskType,
      description: info?.description,
      summary: info?.summary,
      outputFile: info?.outputFile ?? (result ? extractBackgroundOutputFile(result) : undefined),
    };
  }

  // Second pass: build display groups
  const groups: DisplayGroup[] = [];
  const skillSkipIndices = new Set<number>();
  // The "compacting" row of the compaction in progress, if any.
  let compactingGroup: DisplayGroup | null = null;
  for (let idx = 0; idx < entries.length; idx++) {
    const entry = entries[idx];

    // Only entries of the selected thread are shown at the top level. Subagent
    // internals (prompt, assistant turns, tool_results) carry the Agent
    // tool_use id as parent_tool_use_id and are viewed via SubagentThreadView.
    if ((entry.parent_tool_use_id ?? null) !== rootParentToolUseId) {
      continue;
    }

    // Handle system events
    if (entry.type === "system") {
      // task_* events of tasks owned by a subagent belong to that subagent,
      // not to the main timeline. The server annotates them with
      // owned_by_subagent (CLI only flags task_started).
      if (rootParentToolUseId === null) {
        const rawSys = entry as unknown as Record<string, unknown>;
        if (
          rawSys.owned_by_subagent === true &&
          typeof rawSys.subtype === "string" &&
          rawSys.subtype.startsWith("task_")
        ) {
          continue;
        }
      }
      // task_* events of a tool call in the loaded lines are shown on that
      // tool call (background badge) instead of as a standalone row.
      {
        const rawSys = entry as unknown as Record<string, unknown>;
        if (typeof rawSys.subtype === "string" && rawSys.subtype.startsWith("task_")) {
          const toolUseId = resolveTaskToolUseId(rawSys, taskInfoMap);
          if (toolUseId && allToolUseIds.has(toolUseId)) continue;
        }
      }
      // Synthetic entry spliced in by ChoiceCardsStream (never in the real stream).
      {
        const rawSys = entry as unknown as Record<string, unknown>;
        if (rawSys.subtype === "choice_card" && typeof rawSys.card_id === "string") {
          groups.push({ role: "system", items: [{ kind: "choice_card", cardId: rawSys.card_id }] });
          continue;
        }
      }
      // Compaction progress: the CLI emits status=compacting repeatedly while
      // the summary grows (compact_output_chars), then status=null when done.
      // Show a single "compacting" row while it runs and drop it once it ends
      // (compact_boundary records the finished compaction).
      {
        const rawSys = entry as unknown as Record<string, unknown>;
        if (rawSys.subtype === "status" && rawSys.status === "compacting" && compactingGroup) {
          continue;
        }
        if ((rawSys.subtype === "status" || rawSys.subtype === "compact_boundary") && rawSys.status !== "compacting" && compactingGroup) {
          const i = groups.indexOf(compactingGroup);
          if (i >= 0) groups.splice(i, 1);
          compactingGroup = null;
        }
      }
      const sysItem = parseSystemEvent(entry);
      if (sysItem) {
        const group: DisplayGroup = { role: "system", items: [sysItem] };
        groups.push(group);
        if (sysItem.kind === "system_event" && sysItem.eventType === "compacting") {
          compactingGroup = group;
        }
      }
      continue;
    }

    // Handle rate_limit_event
    // The CLI emits one per request; "allowed" means no warning at all, so only
    // non-allowed statuses (allowed_warning / rejected / unknown) are shown.
    // Consecutive events of the same window collapse into one row (latest wins).
    if (entry.type === "rate_limit_event") {
      const raw = entry as unknown as Record<string, unknown>;
      const info = raw.rate_limit_info as Record<string, unknown> | undefined;
      if (info) {
        const status = (info.status as string) || "unknown";
        if (status === "allowed") continue;
        const rateLimitType = (info.rateLimitType as string) || "unknown";
        const windows = info.unifiedWindows as Record<string, Record<string, unknown> | undefined> | undefined;
        const windowUtil = windows?.[rateLimitType]?.utilization;
        const utilization = typeof windowUtil === "number"
          ? windowUtil
          : typeof info.utilization === "number" ? info.utilization : undefined;
        const item: StreamDisplayItem = {
          kind: "rate_limit",
          rateLimitType,
          status,
          resetsAt: (info.resetsAt as number) || 0,
          utilization,
          isUsingOverage: info.isUsingOverage as boolean | undefined,
          overageStatus: info.overageStatus as string | undefined,
        };
        const last = groups[groups.length - 1];
        const lastItem = last && last.role === "system" && last.items.length === 1 ? last.items[0] : undefined;
        if (lastItem && lastItem.kind === "rate_limit" && lastItem.rateLimitType === rateLimitType) {
          groups[groups.length - 1] = { role: "system", items: [item] };
        } else {
          groups.push({ role: "system", items: [item] });
        }
      }
      continue;
    }

    // Handle result event
    if (entry.type === "result") {
      const raw = entry as unknown as Record<string, unknown>;
      const isError = (raw.is_error as boolean) === true;
      const resultText = typeof raw.result === "string" ? raw.result : "";
      const item: StreamDisplayItem = {
        kind: "result",
        isError,
        result: resultText,
        subtype: (raw.subtype as string) || "",
        numTurns: raw.num_turns as number | undefined,
        durationMs: raw.duration_ms as number | undefined,
        costUsd: raw.total_cost_usd as number | undefined,
        terminalReason: typeof raw.terminal_reason === "string" ? raw.terminal_reason : undefined,
        errors: Array.isArray(raw.errors)
          ? (raw.errors as unknown[]).filter((e): e is string => typeof e === "string" && e !== "")
          : undefined,
      };
      // For error results: suppress the immediately preceding assistant group if its
      // text content matches the error message (Claude echoes the error as an assistant message)
      if (isError && resultText) {
        const last = groups[groups.length - 1];
        if (last && last.role === "assistant") {
          const lastTexts = last.items
            .filter((i): i is Extract<StreamDisplayItem, { kind: "text" }> => i.kind === "text")
            .map(i => i.text)
            .join("");
          if (lastTexts === resultText) {
            groups.pop();
          }
        }
      }
      // A turn started by a message from another session (peer) has no user
      // line; the message body arrives only in result.origin. Show it at the
      // start of the turn, i.e. right after the previous result.
      const peerItem = parsePeerOrigin(raw.origin);
      if (peerItem) {
        let insertAt = 0;
        for (let gi = groups.length - 1; gi >= 0; gi--) {
          if (groups[gi].items.some(i => i.kind === "result")) {
            insertAt = gi + 1;
            break;
          }
        }
        groups.splice(insertAt, 0, { role: "system", items: [peerItem] });
      }
      groups.push({ role: "system", items: [item] });
      continue;
    }

    const blocks = parseStreamContentBlocks(entry);

    const role = entry.type as "user" | "assistant";
    const items: StreamDisplayItem[] = [];

    if (entry.type === "assistant") {
      for (let bi = 0; bi < blocks.length; bi++) {
        const b = blocks[bi];
        if (b.type === "thinking" && b.thinking) {
          items.push({ kind: "thinking", text: b.thinking });
        } else if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        } else if (b.type === "tool_use") {
          // Detect plan mode transitions
          if (b.name === "EnterPlanMode") {
            groups.push({ role: "system", items: [{ kind: "system_event", eventType: "plan_enter", label: "プランモードに入りました" }] });
            continue;
          }
          if (b.name === "ExitPlanMode") {
            groups.push({ role: "system", items: [{ kind: "system_event", eventType: "plan_exit", label: "プランモードを終了しました" }] });
            continue;
          }

          let result: string | undefined;
          let resultImages: Array<{ mediaType: string; data: string }> | undefined;

          if (isProviderCoLocatedTools(provider)) {
            // Codex/Cursor: tool_result is in the same assistant content array, directly after tool_use
            if (bi + 1 < blocks.length && blocks[bi + 1].type === "tool_result") {
              const nextBlock = blocks[bi + 1];
              if (typeof nextBlock.content === "string") {
                result = nextBlock.content;
              } else if (nextBlock.content != null) {
                result = JSON.stringify(nextBlock.content);
              }
              bi++; // skip the tool_result block since we consumed it
            }
          } else {
            // Claude: look up result by tool_use_id from user entries
            result = b.id ? resultMap.get(b.id) : undefined;
            resultImages = b.id ? resultImageMap.get(b.id) : undefined;
          }

          // For Skill calls, fold the next user text (skill content) into the result
          if (b.name === "Skill" && b.id && skillToolIds.has(b.id)) {
            // Pattern: tool_use(Skill) → user(tool_result) → user(text = skill content).
            // With parallel tool calls, other tool_use/tool_result entries and
            // non-user entries (rate_limit_event, system, ...) can sit in between.
            let skillContent = result || "";
            const textIndices = findInjectedUserTextIndices(entries, idx, b.id);
            for (const j of textIndices) {
              const textBlocks = parseStreamContentBlocks(entries[j]).filter(nb => nb.type === "text" && nb.text);
              skillContent += "\n---\n" + textBlocks.map(tb => tb.text).join("\n");
              skillSkipIndices.add(j);
            }
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result: skillContent || undefined,
              resultImages,
              toolUseId: b.id,
              running: isRunning(b.id),
              isError: b.id && errorResultIds.has(b.id) ? true : undefined,
              backgroundTask: backgroundTaskFor(b.id),
            });
          } else if (b.name === "ToolSearch" && b.id && toolSearchToolIds.has(b.id)) {
            // Skip the "Tool loaded." user text after ToolSearch
            const [loadedIdx] = findInjectedUserTextIndices(entries, idx, b.id);
            if (loadedIdx !== undefined) skillSkipIndices.add(loadedIdx);
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result,
              resultImages,
              toolUseId: b.id,
              running: isRunning(b.id),
              isError: b.id && errorResultIds.has(b.id) ? true : undefined,
              backgroundTask: backgroundTaskFor(b.id),
            });
          } else if (isAgentToolName(b.name) && b.id && agentToolIds.has(b.id)) {
            // Skip the subagent prompt user text after Agent tool call
            // System entries (task_started, task_progress) may appear between the tool call and the user prompt
            for (let j = idx + 1; j < entries.length && j <= idx + 8; j++) {
              const nextEntry = entries[j];
              if (nextEntry.type === "system") continue; // skip system entries
              if (nextEntry.type !== "user") break;
              const nextBlocks = parseStreamContentBlocks(nextEntry);
              const hasToolResult = nextBlocks.some(nb => nb.type === "tool_result");
              if (hasToolResult) continue;
              const textBlocks = nextBlocks.filter(nb => nb.type === "text" && nb.text);
              if (textBlocks.length > 0) {
                skillSkipIndices.add(j);
                break;
              }
            }
            // Subagent internals are not inlined; they are shown in SubagentThreadView.
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result,
              resultImages,
              toolUseId: b.id,
              running: isRunning(b.id),
              isError: b.id && errorResultIds.has(b.id) ? true : undefined,
              backgroundTask: backgroundTaskFor(b.id),
            });
          } else {
            items.push({
              kind: "tool_call",
              name: b.name ?? "unknown",
              input: b.input,
              result,
              resultImages,
              toolUseId: b.id,
              running: isRunning(b.id),
              isError: b.id && errorResultIds.has(b.id) ? true : undefined,
              backgroundTask: backgroundTaskFor(b.id),
            });
          }
        }
      }
    } else if (entry.type === "user") {
      // Skip user text that was folded into a Skill tool call
      if (skillSkipIndices.has(idx)) {
        continue;
      }

      // CLI-injected text (compaction summary, Stop hook feedback, image scale
      // notes) is not something the user typed; show it as a system row.
      if ((entry as { isSynthetic?: boolean }).isSynthetic) {
        const texts = blocks.filter((b) => b.type === "text" && b.text).map((b) => b.text as string);
        if (texts.length === 0 && typeof entry.message?.content === "string" && entry.message.content) {
          texts.push(entry.message.content);
        }
        const sysItems: StreamDisplayItem[] = texts.map(parseSyntheticUserText);
        for (const b of blocks) {
          if (b.type === "image" && b.source) {
            sysItems.push({ kind: "image", mediaType: b.source.media_type, data: b.source.data });
          }
        }
        if (sysItems.length > 0) {
          const last = groups[groups.length - 1];
          // Never append to the in-progress compacting row: it is removed when compaction ends.
          if (last && last.role === "system" && last !== compactingGroup) last.items.push(...sysItems);
          else groups.push({ role: "system", items: sysItems });
        }
        continue;
      }

      // Only show user text/image content (tool_results are merged into assistant tool_call items)
      for (const b of blocks) {
        if (b.type === "text" && b.text) {
          items.push(parseBashExecText(b.text) ?? { kind: "text", text: b.text });
        } else if (b.type === "image" && b.source) {
          items.push({ kind: "image", mediaType: b.source.media_type, data: b.source.data });
        }
      }
      // user entries with content as plain string
      if (items.length === 0 && typeof entry.message?.content === "string" && entry.message.content) {
        const content = entry.message.content;
        items.push(parseBashExecText(content) ?? { kind: "text", text: content });
      }
    }

    if (items.length > 0) {
      // Merge into previous group if same role
      const last = groups[groups.length - 1];
      if (last && last.role === role) {
        last.items.push(...items);
      } else {
        groups.push({ role, items });
      }
    }
  }

  // Append incremental partial text to the last assistant group (or create one)
  if (partialText) {
    const last = groups[groups.length - 1];
    const partialItem: StreamDisplayItem = { kind: "text", text: partialText };
    if (last && last.role === "assistant") {
      last.items.push(partialItem);
    } else {
      groups.push({ role: "assistant", items: [partialItem] });
    }
  }

  return groups;
}

// --- Consecutive tool call grouping (render-time) ---

type ToolCallItem = Extract<StreamDisplayItem, { kind: "tool_call" }>;

// Tool calls that always get their own row: subagents are important, and the
// interactive/special views (todo list, questions, escalation, permission)
// must stay visible.
const UNGROUPABLE_TOOL_NAMES = new Set([
  "TodoWrite",
  "AskUserQuestion",
  "mcp__agmux__escalate",
  "mcp__agmux__permission_prompt",
]);

export function isGroupableToolCall(item: StreamDisplayItem): item is ToolCallItem {
  return item.kind === "tool_call" && !isAgentToolName(item.name) && !UNGROUPABLE_TOOL_NAMES.has(item.name);
}

export type StreamRenderChunk =
  | { kind: "item"; key: string; item: StreamDisplayItem }
  // items: the tool calls plus any thinking blocks between them (in order)
  | { kind: "tool_group"; key: string; items: StreamDisplayItem[]; toolCalls: ToolCallItem[] };

// Splits a group's items into render chunks, collapsing runs of 2+ consecutive
// groupable tool calls into a single tool_group chunk. Thinking blocks between
// tool calls of a run are absorbed into the run; anything else breaks it.
// Keys are stable while items stream in (a group is keyed by its first tool
// call, which never changes as the run grows).
export function chunkToolCallRuns(items: StreamDisplayItem[]): StreamRenderChunk[] {
  const chunks: StreamRenderChunk[] = [];
  let run: { start: number; items: StreamDisplayItem[]; toolCalls: ToolCallItem[] } | null = null;
  let pending: Array<{ index: number; item: StreamDisplayItem }> = [];

  const flush = () => {
    if (run) {
      if (run.toolCalls.length >= 2) {
        const first = run.toolCalls[0];
        chunks.push({
          kind: "tool_group",
          key: `tg-${first.toolUseId ?? run.start}`,
          items: run.items,
          toolCalls: run.toolCalls,
        });
      } else {
        chunks.push({ kind: "item", key: `i-${run.start}`, item: run.items[0] });
      }
      run = null;
    }
    for (const p of pending) chunks.push({ kind: "item", key: `i-${p.index}`, item: p.item });
    pending = [];
  };

  items.forEach((item, index) => {
    if (isGroupableToolCall(item)) {
      if (run) {
        for (const p of pending) run.items.push(p.item);
        pending = [];
        run.items.push(item);
        run.toolCalls.push(item);
      } else {
        flush();
        run = { start: index, items: [item], toolCalls: [item] };
      }
      return;
    }
    if (item.kind === "thinking" && run) {
      pending.push({ index, item });
      return;
    }
    flush();
    chunks.push({ kind: "item", key: `i-${index}`, item });
  });
  flush();
  return chunks;
}
