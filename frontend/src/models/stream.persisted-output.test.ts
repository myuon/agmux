import { describe, expect, it } from "vitest";
import { mergeStreamEntries, stripPersistedOutputTag, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

const wrapped =
  "<persisted-output>\nOutput too large (34.2KB). Full output saved to: /tmp/tool-results/b0rfhxob9.txt\n\nPreview (first 2KB):\nline1\n...\n</persisted-output>";

describe("stripPersistedOutputTag", () => {
  it("removes the wrapping tags and keeps the inner text", () => {
    expect(stripPersistedOutputTag(wrapped)).toBe(
      "Output too large (34.2KB). Full output saved to: /tmp/tool-results/b0rfhxob9.txt\n\nPreview (first 2KB):\nline1\n...",
    );
  });

  it("keeps text that follows the closing tag", () => {
    expect(stripPersistedOutputTag(`${wrapped}\nShell cwd was reset to /repo`)).toBe(
      "Output too large (34.2KB). Full output saved to: /tmp/tool-results/b0rfhxob9.txt\n\nPreview (first 2KB):\nline1\n...\nShell cwd was reset to /repo",
    );
  });

  it("leaves text without the wrapping tag unchanged", () => {
    expect(stripPersistedOutputTag("ok")).toBe("ok");
    expect(stripPersistedOutputTag("prefix <persisted-output>x</persisted-output>")).toBe(
      "prefix <persisted-output>x</persisted-output>",
    );
  });
});

describe("tool_result with <persisted-output>", () => {
  const toolUse = {
    type: "assistant",
    message: { role: "assistant", content: [{ type: "tool_use", id: "t1", name: "Bash", input: { command: "cat big.log" } }] },
  };
  const findResult = (content: unknown) => {
    const groups = mergeStreamEntries(
      asEntries([
        toolUse,
        { type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "t1", content }] } },
      ]),
    );
    return groups.flatMap((g) => g.items).find((i) => i.kind === "tool_call");
  };

  it("strips the tags from string content", () => {
    const item = findResult(wrapped);
    expect(item).toMatchObject({ kind: "tool_call" });
    expect((item as { result?: string }).result).not.toContain("persisted-output");
    expect((item as { result?: string }).result).toContain("Output too large (34.2KB)");
  });

  it("strips the tags from text-block array content", () => {
    const item = findResult([{ type: "text", text: wrapped }]);
    expect((item as { result?: string }).result).not.toContain("persisted-output");
  });
});
