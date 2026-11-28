import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

function findToolCall(lines: unknown[]) {
  const groups = mergeStreamEntries(asEntries(lines));
  for (const g of groups) {
    for (const item of g.items) {
      if (item.kind === "tool_call") return item;
    }
  }
  throw new Error("tool_call not found");
}

describe("tool_result content blocks", () => {
  it("shows tool_reference blocks as the loaded tool names", () => {
    const item = findToolCall([
      { type: "assistant", message: { role: "assistant", content: [{ type: "tool_use", id: "toolu_1", name: "ToolSearch", input: { query: "select:a,b" } }] }, parent_tool_use_id: null },
      {
        type: "user",
        message: {
          role: "user",
          content: [{
            type: "tool_result",
            tool_use_id: "toolu_1",
            content: [
              { type: "tool_reference", tool_name: "mcp__agmux__create_goal" },
              { type: "tool_reference", tool_name: "mcp__agmux__complete_goal" },
            ],
          }],
        },
        parent_tool_use_id: null,
      },
    ]);
    expect(item).toMatchObject({ result: "mcp__agmux__create_goal\nmcp__agmux__complete_goal" });
  });

  it("shows a placeholder for document blocks next to the text", () => {
    const item = findToolCall([
      { type: "assistant", message: { role: "assistant", content: [{ type: "tool_use", id: "toolu_2", name: "Read", input: { file_path: "/x.pdf" } }] }, parent_tool_use_id: null },
      {
        type: "user",
        message: {
          role: "user",
          content: [{
            type: "tool_result",
            tool_use_id: "toolu_2",
            content: [
              { type: "text", text: "PDF file read: /x.pdf (1.5MB)" },
              { type: "document", source: { type: "base64", media_type: "application/pdf", data: "JVBERi0=" } },
            ],
          }],
        },
        parent_tool_use_id: null,
      },
    ]);
    expect(item).toMatchObject({ result: "PDF file read: /x.pdf (1.5MB)\n[document: application/pdf]" });
  });
});
