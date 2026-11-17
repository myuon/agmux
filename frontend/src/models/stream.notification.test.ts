import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system notification event", () => {
  it("shows the stop-hook-error notification without the TUI key hint", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "notification",
          key: "stop-hook-error",
          text: "stop hook reported a problem · ctrl+o to see",
          priority: "immediate",
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "notification",
      label: "stop hook reported a problem",
    });
  });

  it("skips a notification without text", () => {
    const groups = mergeStreamEntries(asEntries([{ type: "system", subtype: "notification", key: "x" }]));
    expect(groups.flatMap((g) => g.items)).toHaveLength(0);
  });
});
