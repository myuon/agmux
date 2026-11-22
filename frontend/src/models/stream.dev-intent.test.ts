import { describe, expect, it } from "vitest";
import { HIDDEN_SYSTEM_SUBTYPES, mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system dev_intent event", () => {
  it("is intentionally hidden", () => {
    expect(HIDDEN_SYSTEM_SUBTYPES.has("dev_intent")).toBe(true);
    const groups = mergeStreamEntries(
      asEntries([
        { type: "system", subtype: "dev_intent", kind: "ios_app", trigger: "ios_command", uuid: "u1", session_id: "s1" },
      ]),
    );
    expect(groups.flatMap((g) => g.items)).toHaveLength(0);
  });
});
