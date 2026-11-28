import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];
const userText = (text: string, extra: object = {}) => ({
  type: "user",
  message: { role: "user", content: [{ type: "text", text }] },
  parent_tool_use_id: null,
  ...extra,
});

describe("user interrupted entries", () => {
  it("renders [Request interrupted by user] as a system event", () => {
    const groups = mergeStreamEntries(asEntries([userText("[Request interrupted by user]")]));
    expect(groups).toHaveLength(1);
    expect(groups[0].role).toBe("system");
    expect(groups[0].items[0]).toEqual({
      kind: "system_event",
      eventType: "user_interrupted",
      label: "ユーザーが中断しました",
    });
  });

  it("renders the tool use variant with its own label", () => {
    const groups = mergeStreamEntries(asEntries([userText("[Request interrupted by user for tool use]")]));
    expect(groups[0].role).toBe("system");
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "user_interrupted",
      label: "ユーザーがツール実行中に中断しました",
    });
  });

  it("handles plain string content", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "user", message: { role: "user", content: "[Request interrupted by user]" } }]),
    );
    expect(groups[0].role).toBe("system");
  });

  it("handles subagent entries with parent_tool_use_id", () => {
    const groups = mergeStreamEntries(
      asEntries([userText("[Request interrupted by user]", { parent_tool_use_id: "toolu_1" })]),
      undefined,
      undefined,
      "toolu_1",
    );
    expect(groups[0].role).toBe("system");
  });

  it("keeps normal user text as a user bubble", () => {
    const groups = mergeStreamEntries(asEntries([userText("hello"), userText("[Request interrupted by user]")]));
    expect(groups.map((g) => g.role)).toEqual(["user", "system"]);
    expect(groups[0].items[0]).toMatchObject({ kind: "text", text: "hello" });
  });

  it("does not match text that merely contains the marker", () => {
    const groups = mergeStreamEntries(asEntries([userText("please see [Request interrupted by user] here")]));
    expect(groups[0].role).toBe("user");
  });
});
