import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system hook_response event", () => {
  it("shows a failed hook with its name, exit code and stderr", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "hook_response",
          hook_name: "SessionStart:startup",
          hook_event: "SessionStart",
          output: "out\nSyntaxError: Unexpected token '?'\n",
          stdout: "",
          stderr: "SyntaxError: Unexpected token '?'\n",
          exit_code: 1,
          outcome: "error",
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "hook_error",
      label: "⚠ フック SessionStart:startup が失敗しました (exit 1)",
      detail: "SyntaxError: Unexpected token '?'",
    });
  });

  it("skips successful hooks and hook_started", () => {
    const groups = mergeStreamEntries(
      asEntries([
        { type: "system", subtype: "hook_started", hook_name: "SessionStart:startup" },
        { type: "system", subtype: "hook_response", hook_name: "SessionStart:startup", output: "", exit_code: 0, outcome: "success" },
      ]),
    );
    expect(groups.flatMap((g) => g.items)).toHaveLength(0);
  });
});
