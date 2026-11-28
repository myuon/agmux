import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("error result without result text", () => {
  it("keeps terminal_reason and errors for error_during_execution", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "result",
          subtype: "error_during_execution",
          is_error: true,
          num_turns: 3,
          stop_reason: "tool_use",
          terminal_reason: "aborted_streaming",
          errors: ["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"],
        },
      ]),
    );
    expect(groups[0].items[0]).toMatchObject({
      kind: "result",
      isError: true,
      result: "",
      subtype: "error_during_execution",
      terminalReason: "aborted_streaming",
      errors: ["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"],
    });
  });

  it("ignores non-string errors", () => {
    const groups = mergeStreamEntries(asEntries([{ type: "result", subtype: "error_max_turns", is_error: true, errors: [1, "", "x"] }]));
    expect(groups[0].items[0]).toMatchObject({ errors: ["x"], terminalReason: undefined });
  });
});

describe("error result echoed as assistant text", () => {
  const err = "API Error: 500 Internal server error";
  const toolTurn = [
    { type: "assistant", message: { role: "assistant", content: [{ type: "thinking", thinking: "hmm" }, { type: "tool_use", id: "t1", name: "Bash", input: { command: "ls" } }] } },
    { type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "t1", content: "ok" }] } },
  ];

  it("removes only the echoed text and keeps tool calls in the same group", () => {
    const groups = mergeStreamEntries(
      asEntries([
        ...toolTurn,
        { type: "assistant", message: { role: "assistant", content: [{ type: "text", text: err }] } },
        { type: "result", subtype: "success", is_error: true, result: err },
      ]),
    );
    const assistant = groups.find(g => g.role === "assistant");
    expect(assistant).toBeDefined();
    const kinds = assistant!.items.map(i => i.kind);
    expect(kinds).toContain("tool_call");
    expect(kinds).toContain("thinking");
    expect(kinds).not.toContain("text");
    expect(groups[groups.length - 1].items[0]).toMatchObject({ kind: "result", result: err });
  });

  it("drops the group when it only contains the echoed text", () => {
    const groups = mergeStreamEntries(
      asEntries([
        { type: "assistant", message: { role: "assistant", content: [{ type: "text", text: err }] } },
        { type: "result", subtype: "success", is_error: true, result: err },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({ kind: "result" });
  });

  it("keeps the group untouched when text does not match", () => {
    const groups = mergeStreamEntries(
      asEntries([
        ...toolTurn,
        { type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "other" }] } },
        { type: "result", subtype: "success", is_error: true, result: err },
      ]),
    );
    const assistant = groups.find(g => g.role === "assistant")!;
    expect(assistant.items.some(i => i.kind === "text")).toBe(true);
  });
});
