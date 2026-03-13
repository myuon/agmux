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
  | { kind: "tool_call"; name: string; input: unknown; result?: string; resultImages?: Array<{ mediaType: string; data: string }>; toolUseId?: string; children?: StreamDisplayItem[] }
  | { kind: "thinking"; text: string }
  | { kind: "system_event"; eventType: string; label: string; detail?: string };

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

export type DisplayGroup =
  | { role: "user" | "assistant"; items: StreamDisplayItem[] }
  | { role: "system"; items: StreamDisplayItem[] };

// Merge assistant/user entries into display items, pairing tool_use with tool_result by id
// partialText: incremental text from stream_event deltas (shown as "typing" in the last assistant group)
export function mergeStreamEntries(entries: StreamEntry[], partialText?: string): DisplayGroup[] {
  // First pass: collect all tool_results keyed by tool_use_id, and track Skill tool IDs
  const resultMap = new Map<string, string>();
  const resultImageMap = new Map<string, Array<{ mediaType: string; data: string }>>();
  const skillToolIds = new Set<string>();
  const toolSearchToolIds = new Set<string>();
  const askUserToolIds = new Set<string>();
  const agentToolIds = new Set<string>();
  // Collect child entries grouped by parent_tool_use_id
  const childEntriesMap = new Map<string, StreamEntry[]>();
  for (const entry of entries) {
    // Track entries with parent_tool_use_id
    if (entry.parent_tool_use_id) {
      const children = childEntriesMap.get(entry.parent_tool_use_id) || [];
      children.push(entry);
      childEntriesMap.set(entry.parent_tool_use_id, children);
    }
    if (entry.type === "assistant") {
      for (const block of parseStreamContentBlocks(entry)) {
        if (block.type === "tool_use" && block.name === "Skill" && block.id) {
          skillToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "ToolSearch" && block.id) {
          toolSearchToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "AskUserQuestion" && block.id) {
          askUserToolIds.add(block.id);
        }
        if (block.type === "tool_use" && block.name === "Agent" && block.id) {
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

  // Helper: build child tool_call items from child entries of an Agent
  function buildChildItems(parentToolUseId: string): StreamDisplayItem[] {
    const childEntries = childEntriesMap.get(parentToolUseId) || [];
    const items: StreamDisplayItem[] = [];
    for (const entry of childEntries) {
      if (entry.type !== "assistant") continue;
      for (const b of parseStreamContentBlocks(entry)) {
        if (b.type === "tool_use" && b.name) {
          const result = b.id ? resultMap.get(b.id) : undefined;
          const resultImages = b.id ? resultImageMap.get(b.id) : undefined;
          // Recursively build children if this is also an Agent
          const children = (b.name === "Agent" && b.id && childEntriesMap.has(b.id))
            ? buildChildItems(b.id)
            : undefined;
          items.push({
            kind: "tool_call",
            name: b.name,
            input: b.input,
            result,
            resultImages,
            toolUseId: b.id,
            children: children && children.length > 0 ? children : undefined,
          });
        } else if (b.type === "thinking" && b.thinking) {
          items.push({ kind: "thinking", text: b.thinking });
        } else if (b.type === "text" && b.text) {
          items.push({ kind: "text", text: b.text });
        }
      }
    }
    return items;
  }

  // Second pass: build display groups
  const groups: DisplayGroup[] = [];
  const skillSkipIndices = new Set<number>();
  for (let idx = 0; idx < entries.length; idx++) {
    const entry = entries[idx];

    // Skip entries that belong to a parent Agent that is present in the current data
    if (entry.parent_tool_use_id && agentToolIds.has(entry.parent_tool_use_id)) {
      continue;
    }

    // Handle system events
    if (entry.type === "system") {
      const sysItem = parseSystemEvent(entry);
      if (sysItem) {
        groups.push({ role: "system", items: [sysItem] });
      }
      continue;
    }

    const blocks = parseStreamContentBlocks(entry);

    const role = entry.type as "user" | "assistant";
    const items: StreamDisplayItem[] = [];

    if (entry.type === "assistant") {
      for (const b of blocks) {
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

          const result = b.id ? resultMap.get(b.id) : undefined;
          const resultImages = b.id ? resultImageMap.get(b.id) : undefined;

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
            });
          } else if (b.name === "Agent" && b.id && agentToolIds.has(b.id)) {
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
            // Build child items from entries with parent_tool_use_id matching this Agent
            const children = buildChildItems(b.id);
            items.push({
              kind: "tool_call",
              name: b.name,
              input: b.input,
              result,
              resultImages,
              toolUseId: b.id,
              children: children.length > 0 ? children : undefined,
            });
          } else {
            items.push({
              kind: "tool_call",
              name: b.name ?? "unknown",
              input: b.input,
              result,
              resultImages,
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
          items.push({ kind: "text", text: b.text });
        } else if (b.type === "image" && b.source) {
          items.push({ kind: "image", mediaType: b.source.media_type, data: b.source.data });
        }
      }
      // user entries with content as plain string
      if (items.length === 0 && typeof entry.message?.content === "string" && entry.message.content) {
        items.push({ kind: "text", text: entry.message.content });
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
