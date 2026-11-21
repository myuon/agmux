import { describe, expect, it } from "vitest";
import { HIDDEN_SYSTEM_SUBTYPES, mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system hook_progress event", () => {
  it("is intentionally hidden", () => {
    expect(HIDDEN_SYSTEM_SUBTYPES.has("hook_progress")).toBe(true);
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "hook_progress",
          hook_id: "h1",
          hook_name: "SessionStart:startup",
          hook_event: "SessionStart",
          stdout: "",
          stderr: "(node:1) ExperimentalWarning: The ESM module loader is experimental.\n",
          output: "(node:1) ExperimentalWarning: The ESM module loader is experimental.\n",
          uuid: "u1",
          session_id: "s1",
        },
      ]),
    );
    expect(groups.flatMap((g) => g.items)).toHaveLength(0);
  });
});
