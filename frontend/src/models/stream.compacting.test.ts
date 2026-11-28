import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

const compacting = (chars: number) => ({ type: "system", subtype: "status", status: "compacting", compact_output_chars: chars });
const done = { type: "system", subtype: "status", status: null, compact_result: "success" };
const boundary = { type: "system", subtype: "compact_boundary", compact_metadata: { trigger: "auto", pre_tokens: 970846 } };

const eventTypes = (entries: unknown[]) =>
  mergeStreamEntries(asEntries(entries))
    .flatMap((g) => g.items)
    .map((i) => (i.kind === "system_event" ? i.eventType : i.kind));

describe("compaction status events", () => {
  it("shows a single compacting row while progress events keep arriving", () => {
    expect(eventTypes([compacting(0), compacting(6897), compacting(12734)])).toEqual(["compacting"]);
  });

  it("drops the compacting row once compaction finishes", () => {
    expect(eventTypes([compacting(0), compacting(6897), compacting(12734), done, boundary])).toEqual(["compact"]);
  });

  it("drops the compacting row even when other events are interleaved", () => {
    const user = { type: "user", message: { role: "user", content: "hi" } };
    expect(eventTypes([compacting(0), user, compacting(100), done, boundary])).toEqual(["text", "compact"]);
  });

  it("shows a new row for each compaction", () => {
    expect(
      eventTypes([compacting(0), compacting(10), done, boundary, compacting(0), compacting(10)]),
    ).toEqual(["compact", "compacting"]);
  });
});
