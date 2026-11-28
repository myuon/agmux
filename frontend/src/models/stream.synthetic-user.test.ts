import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];
const synthetic = (text: string) => ({
  type: "user",
  isSynthetic: true,
  message: { role: "user", content: [{ type: "text", text }] },
});

describe("isSynthetic user text", () => {
  it("shows the compaction summary as a system row, not a user bubble", () => {
    const text = "This session is being continued from a previous conversation that ran out of context.\nSummary...";
    const groups = mergeStreamEntries(asEntries([synthetic(text)]));
    expect(groups).toEqual([
      { role: "system", items: [{ kind: "system_event", eventType: "compact_summary", label: "コンパクション後の要約", detail: text }] },
    ]);
  });

  it("keeps the Stop hook feedback body visible", () => {
    const groups = mergeStreamEntries(asEntries([synthetic("Stop hook feedback:\nSTATE.md を更新してください")]));
    expect(groups[0].role).toBe("system");
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "stop_hook_feedback",
      label: "Stop フックのフィードバック",
      detail: "STATE.md を更新してください",
    });
  });

  it("labels the image scale note", () => {
    const text = "[Image: original 2880x1280, displayed at 2576x1145. Multiply coordinates by 1.12 to map to original image.]";
    const groups = mergeStreamEntries(asEntries([synthetic(text)]));
    expect(groups[0]).toMatchObject({ role: "system", items: [{ eventType: "image_scale_note", detail: text }] });
  });

  it("still shows ordinary user text as a user bubble", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "user", message: { role: "user", content: [{ type: "text", text: "hello" }] } }]),
    );
    expect(groups).toEqual([{ role: "user", items: [{ kind: "text", text: "hello" }] }]);
  });
});
