import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system informational event", () => {
  it("shows the hook_no_verdict warning", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "informational",
          content: "A hook didn't answer in time, so it didn't check this action.",
          level: "warning",
          tag: "hook_no_verdict",
          hook_no_verdict: { hook_event: "PreToolUse", tool_use_id: "toolu_x", timed_out: true },
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "informational",
      label: "⚠ A hook didn't answer in time, so it didn't check this action.",
    });
  });

  it("shows info-level content without the warning mark", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "system", subtype: "informational", content: "hello", level: "info" }]),
    );
    expect(groups[0].items[0]).toMatchObject({ kind: "system_event", label: "hello" });
  });

  it("skips an informational event without content", () => {
    const groups = mergeStreamEntries(asEntries([{ type: "system", subtype: "informational", level: "warning" }]));
    expect(groups.flatMap((g) => g.items)).toHaveLength(0);
  });
});
