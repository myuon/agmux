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
  | { kind: "api_retry"; attempt: number; maxRetries: number; retryDelayMs: number; errorStatus?: number; error?: string }
  | { kind: "result"; isError: boolean; result: string; subtype: string; numTurns?: number; durationMs?: number; costUsd?: number };

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

// Tool call history entry for tracking sub-agent activity
export interface ToolCallHistoryEntry {
  toolName: string;
  input?: unknown;
  description?: string;
  timestamp?: string;
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
  toolCallHistory: ToolCallHistoryEntry[];
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
        const task: ActiveTask = { taskId, taskType: taskType || "unknown", toolCallHistory: [] };
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
          const task: ActiveTask = { taskId, taskType, toolCallHistory: [] };
          if (raw.agent_id) task.agentId = raw.agent_id as string;
          tasks.set(taskId, task);
        }
        const task = tasks.get(taskId)!;
        if (raw.description) task.description = raw.description as string;
        // Track tool call history: append when last_tool_name changes
        if (raw.last_tool_name) {
          const newToolName = raw.last_tool_name as string;
          const newDescription = raw.description as string | undefined;
          const lastEntry = task.toolCallHistory[task.toolCallHistory.length - 1];
          if (!lastEntry || lastEntry.toolName !== newToolName || lastEntry.description !== newDescription) {
            task.toolCallHistory.push({
              toolName: newToolName,
              description: newDescription,
              timestamp: raw.timestamp as string | undefined,
            });
          }
          task.lastToolName = newToolName;
        }
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
function parseSystemEvent(entry: StreamEntry): StreamDisplayItem | null {
  const raw = entry as unknown as Record<string, unknown>;
  const subtype = raw.subtype as string | undefined;

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

  if (subtype === "task_notification") {
    const status = raw.status as string;
    const summary = (raw.summary as string) || "";
    return {
      kind: "system_event",
      eventType: "task_notification",
      label: `タスク${status === "completed" ? "完了" : status}`,
      detail: summary || undefined,
    };
  }

  return null;
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
          for (const b of block.content as Array<{ type?: string; text?: string; source?: { media_type: string; data: string } }>) {
            if (b.type === "image" && b.source) {
              images.push({ mediaType: b.source.media_type, data: b.source.data });
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
      const sysItem = parseSystemEvent(entry);
      if (sysItem) {
        groups.push({ role: "system", items: [sysItem] });
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
            // Pattern: tool_use(Skill) → user(tool_result) → user(text = skill content)
            // Scan forward past tool_result entries to find the text-only user entry
            let skillContent = result || "";
            for (let j = idx + 1; j < entries.length && j <= idx + 4; j++) {
              const nextEntry = entries[j];
              if (nextEntry.type !== "user") break;
              const nextBlocks = parseStreamContentBlocks(nextEntry);
              const textBlocks = nextBlocks.filter(nb => nb.type === "text" && nb.text);
              const hasToolResult = nextBlocks.some(nb => nb.type === "tool_result");
              if (hasToolResult) continue; // skip the tool_result entry
              if (textBlocks.length > 0) {
                skillContent += "\n---\n" + textBlocks.map(tb => tb.text).join("\n");
                skillSkipIndices.add(j);
                break;
              }
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
            for (let j = idx + 1; j < entries.length && j <= idx + 4; j++) {
              const nextEntry = entries[j];
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
