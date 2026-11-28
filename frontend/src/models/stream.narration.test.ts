import { describe, expect, it } from "vitest";
import { chunkToolCallRuns, mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("narration thinking blocks", () => {
  it("marks thinking blocks listed in narration_block_indexes as narration", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "assistant",
          message: {
            content: [
              { type: "thinking", thinking: "次は hourly.jsonl に追記します。", signature: "x" },
              { type: "thinking", thinking: "ordinary reasoning", signature: "y" },
            ],
          },
          narration_block_indexes: [0],
        },
      ]),
    );
    const items = groups.flatMap((g) => g.items);
    expect(items[0]).toEqual({ kind: "thinking", text: "次は hourly.jsonl に追記します。", narration: true });
    expect(items[1]).toEqual({ kind: "thinking", text: "ordinary reasoning" });
  });

  it("keeps thinking ordinary when the field is absent", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "assistant", message: { content: [{ type: "thinking", thinking: "t" }] } }]),
    );
    expect(groups[0].items[0]).toEqual({ kind: "thinking", text: "t" });
  });

  it("does not fold narration into a tool call run", () => {
    const tool = (id: string) => ({ kind: "tool_call", toolName: "Read", toolUseId: id, input: {} }) as never;
    const chunks = chunkToolCallRuns([
      tool("a"),
      { kind: "thinking", text: "narr", narration: true },
      tool("b"),
    ]);
    expect(chunks.some((c) => c.kind === "item" && c.item.kind === "thinking")).toBe(true);
  });
});
