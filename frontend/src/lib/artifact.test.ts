import { describe, expect, it } from "vitest";
import { artifactRawUrl, parseArtifactUrl } from "./artifact";

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

const artifactId = "e56d11d264cb2ac1987978e3b830968c";

describe("parseArtifactUrl", () => {
  it("matches absolute URLs with any host/port", () => {
    expect(parseArtifactUrl(`http://localhost:4321/artifacts/${artifactId}`)).toBe(artifactId);
    expect(parseArtifactUrl(`https://example.com/artifacts/${artifactId}/`)).toBe(artifactId);
  });
  it("matches relative paths", () => {
    expect(parseArtifactUrl(`/artifacts/${artifactId}`)).toBe(artifactId);
  });
  it("rejects others", () => {
    expect(parseArtifactUrl(undefined)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/${artifactId}/raw`)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/xyz`)).toBeNull();
    expect(parseArtifactUrl(`http://a.com/other/${artifactId}`)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/${artifactId}0`)).toBeNull();
  });
});
