import { describe, expect, it } from "vitest";
import { READ_RING, lampClass, lampLabel, statusDots } from "./StatusBadge";

describe("lampClass (#859)", () => {
  it("shows a faint ring for an idle session that has been read", () => {
    expect(lampClass("idle", false)).toBe(READ_RING);
    expect(lampClass("idle", undefined)).toBe(READ_RING);
    expect(lampLabel("idle", false)).toBeUndefined();
  });

  it("shows the blue lamp for an idle session with an unread turn", () => {
    expect(lampClass("idle", true)).toBe(statusDots.idle);
    expect(lampLabel("idle", true)).toBe("未読");
  });

  it("keeps the other statuses unchanged regardless of unread", () => {
    for (const s of ["working", "paused", "exited", "waiting_input", "archived"] as const) {
      expect(lampClass(s, false)).toBe(statusDots[s]);
      expect(lampClass(s, true)).toBe(statusDots[s]);
      expect(lampLabel(s, true)).toBeUndefined();
    }
  });

  it("falls back to gray for an unknown status", () => {
    expect(lampClass("weird", true)).toBe("bg-gray-300");
  });
});
