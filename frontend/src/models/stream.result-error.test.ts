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
