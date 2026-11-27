import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

const base = { type: "result", subtype: "success", duration_ms: 217, total_cost_usd: 0 };

describe("empty zero-turn result", () => {
  it("hides a successful 0-turn result with empty text", () => {
    const groups = mergeStreamEntries(
      asEntries([{ ...base, is_error: false, num_turns: 0, result: "", origin: { kind: "task-notification" } }]),
    );
    expect(groups).toHaveLength(0);
  });

  it("shows a 0-turn result with non-empty text", () => {
    const groups = mergeStreamEntries(asEntries([{ ...base, is_error: false, num_turns: 0, result: "hello" }]));
    expect(groups[0].items[0]).toMatchObject({ kind: "result", result: "hello" });
  });

  it("shows an is_error 0-turn empty result", () => {
    const groups = mergeStreamEntries(asEntries([{ ...base, is_error: true, num_turns: 0, result: "" }]));
    expect(groups[0].items[0]).toMatchObject({ kind: "result", isError: true });
  });

  it("shows a normal multi-turn result", () => {
    const groups = mergeStreamEntries(asEntries([{ ...base, is_error: false, num_turns: 3, result: "" }]));
    expect(groups[0].items[0]).toMatchObject({ kind: "result", numTurns: 3 });
  });
});
