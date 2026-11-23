import { describe, expect, it } from "vitest";
import { splitStopped } from "./ProjectRow";

describe("splitStopped (#873)", () => {
  it("separates stopped automations and keeps order", () => {
    const [active, stopped] = splitStopped([
      { id: "a", state: "waiting" },
      { id: "b", state: "stopped" },
      { id: "c", state: "running" },
      { id: "d", state: "stopped" },
    ]);
    expect(active.map((x) => x.id)).toEqual(["a", "c"]);
    expect(stopped.map((x) => x.id)).toEqual(["b", "d"]);
  });

  it("returns an empty stopped list when none are stopped", () => {
    expect(splitStopped([{ state: "waiting" }])[1]).toEqual([]);
  });
});
