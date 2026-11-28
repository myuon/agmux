import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

const assistant = (text: string) => ({
  type: "assistant",
  message: { role: "assistant", content: [{ type: "text", text }] },
});

describe("peer origin on result", () => {
  it("shows the peer message at the start of the turn it started", () => {
    const groups = mergeStreamEntries(
      asEntries([
        { type: "user", message: { role: "user", content: [{ type: "text", text: "hello" }] } },
        assistant("hi"),
        { type: "result", subtype: "success", is_error: false, result: "hi" },
        assistant("別のセッションから頼まれた作業を始めました"),
        {
          type: "result",
          subtype: "success",
          is_error: false,
          result: "done",
          origin: { kind: "peer", name: "x-signal-lab-70", body: "x-signal-lab からの依頼です。\n検証してください" },
        },
      ]),
    );
    const resultIdx = groups.findIndex((g) => g.items[0].kind === "result");
    const peer = groups[resultIdx + 1];
    expect(peer.items[0]).toMatchObject({
      kind: "system_event",
      eventType: "peer_message",
      label: "他のセッション（x-signal-lab-70）からのメッセージ",
      detail: "x-signal-lab からの依頼です。\n検証してください",
    });
    expect(groups[resultIdx + 2].role).toBe("assistant");
  });

  it("puts the peer message first when no earlier result is loaded", () => {
    const groups = mergeStreamEntries(
      asEntries([
        assistant("working"),
        { type: "result", subtype: "success", is_error: false, result: "ok", origin: { kind: "peer", body: "please" } },
      ]),
    );
    expect(groups[0].items[0]).toMatchObject({ eventType: "peer_message", label: "他のセッションからのメッセージ", detail: "please" });
  });

  it("ignores non-peer origins", () => {
    const groups = mergeStreamEntries(
      asEntries([
        assistant("x"),
        { type: "result", subtype: "success", is_error: false, result: "x", origin: { kind: "task-notification" } },
      ]),
    );
    expect(groups.flatMap((g) => g.items).some((i) => i.kind === "system_event")).toBe(false);
  });
});
