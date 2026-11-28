import { describe, expect, it } from "vitest";
import { mergeStreamEntries, stripToolUseErrorTag, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("stripToolUseErrorTag", () => {
  it("removes the wrapping tag and keeps the inner text", () => {
    expect(stripToolUseErrorTag("<tool_use_error>Error: No such tool available: bash</tool_use_error>")).toBe(
      "Error: No such tool available: bash",
    );
  });

  it("keeps multi-line inner text", () => {
    expect(stripToolUseErrorTag("<tool_use_error>line1\nline2</tool_use_error>\n")).toBe("line1\nline2");
  });

  it("leaves text without the wrapping tag unchanged", () => {
    expect(stripToolUseErrorTag("ok")).toBe("ok");
    expect(stripToolUseErrorTag("prefix <tool_use_error>x</tool_use_error>")).toBe("prefix <tool_use_error>x</tool_use_error>");
  });
});

describe("tool_result with <tool_use_error>", () => {
  const toolUse = {
    type: "assistant",
    message: { role: "assistant", content: [{ type: "tool_use", id: "t1", name: "bash", input: {} }] },
  };

  it("strips the tag from string content", () => {
    const groups = mergeStreamEntries(
      asEntries([
        toolUse,
        {
          type: "user",
          message: {
            role: "user",
            content: [{ type: "tool_result", tool_use_id: "t1", is_error: true, content: "<tool_use_error>Error: No such tool available: bash</tool_use_error>" }],
          },
        },
      ]),
    );
    const item = groups.flatMap((g) => g.items).find((i) => i.kind === "tool_call");
    expect(item).toMatchObject({ kind: "tool_call", isError: true, result: "Error: No such tool available: bash" });
  });

  it("strips the tag from text-block array content", () => {
    const groups = mergeStreamEntries(
      asEntries([
        toolUse,
        {
          type: "user",
          message: {
            role: "user",
            content: [{ type: "tool_result", tool_use_id: "t1", is_error: true, content: [{ type: "text", text: "<tool_use_error>Blocked</tool_use_error>" }] }],
          },
        },
      ]),
    );
    const item = groups.flatMap((g) => g.items).find((i) => i.kind === "tool_call");
    expect(item).toMatchObject({ kind: "tool_call", result: "Blocked" });
  });
});
