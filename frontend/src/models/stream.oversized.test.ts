import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("oversized stream lines", () => {
  it("shows a notice for the holder's oversized_output system event", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "oversized_output",
          message: "出力が大きすぎるため省略しました（16.7MB, type=assistant）",
          parent_tool_use_id: null,
          oversized_output: { bytes: 17_500_000, original_type: "assistant" },
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].role).toBe("system");
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "oversized_output",
      label: "出力が大きすぎるため省略しました（16.7MB, type=assistant）",
    });
  });

  it("shows the oversized_input notice", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "system", subtype: "oversized_input", message: "送信メッセージが大きすぎるため CLI に渡せませんでした（12.0MB）" }]),
    );
    expect(groups[0].items[0]).toMatchObject({ kind: "system_event", eventType: "oversized_input" });
  });

  it("a replaced tool_result stops the tool call from running", () => {
    const groups = mergeStreamEntries(
      asEntries([
        { type: "assistant", message: { role: "assistant", content: [{ type: "tool_use", id: "toolu_1", name: "Read", input: { file_path: "/x.pdf" } }] }, parent_tool_use_id: null },
        {
          type: "user",
          message: { role: "user", content: [{ type: "tool_result", tool_use_id: "toolu_1", is_error: true, content: "出力が大きすぎるため省略しました（16.7MB, type=user）" }] },
          parent_tool_use_id: null,
          oversized_output: { bytes: 17_500_000, original_type: "user" },
        },
      ]),
    );
    const tool = groups.flatMap((g) => g.items).find((i) => i.kind === "tool_call");
    expect(tool).toMatchObject({ kind: "tool_call", result: expect.stringContaining("出力が大きすぎる"), isError: true });
    expect(tool && tool.kind === "tool_call" ? tool.running : false).toBeFalsy();
  });

  it("a replaced result line renders as a result item", () => {
    const groups = mergeStreamEntries(
      asEntries([{ type: "result", subtype: "success", is_error: false, result: "出力が大きすぎるため省略しました（16.7MB, type=result）" }]),
    );
    expect(groups[0].items[0]).toMatchObject({ kind: "result", subtype: "success" });
  });
});
