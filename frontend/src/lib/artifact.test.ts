import { describe, expect, it } from "vitest";
import { artifactRawUrl } from "./artifact";

describe("artifactRawUrl", () => {
  const id = "a".repeat(32);

  it("has no query without updatedAt", () => {
    expect(artifactRawUrl(id)).toBe(`/artifacts/${id}/raw`);
  });

  it("changes when updatedAt changes so the iframe reloads", () => {
    const a = artifactRawUrl(id, "2026-11-05T00:00:00.1Z");
    const b = artifactRawUrl(id, "2026-11-05T00:00:00.2Z");
    expect(a).not.toBe(b);
    expect(a.startsWith(`/artifacts/${id}/raw?v=`)).toBe(true);
  });

  it("encodes the value", () => {
    expect(artifactRawUrl(id, "a b&c")).toBe(`/artifacts/${id}/raw?v=a%20b%26c`);
  });
});
