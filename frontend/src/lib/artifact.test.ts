import { describe, expect, it } from "vitest";
import { parseArtifactUrl } from "./artifact";

const id = "e56d11d264cb2ac1987978e3b830968c";

describe("parseArtifactUrl", () => {
  it("matches absolute URLs with any host/port", () => {
    expect(parseArtifactUrl(`http://localhost:4321/artifacts/${id}`)).toBe(id);
    expect(parseArtifactUrl(`https://example.com/artifacts/${id}/`)).toBe(id);
  });
  it("matches relative paths", () => {
    expect(parseArtifactUrl(`/artifacts/${id}`)).toBe(id);
  });
  it("rejects others", () => {
    expect(parseArtifactUrl(undefined)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/${id}/raw`)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/xyz`)).toBeNull();
    expect(parseArtifactUrl(`http://a.com/other/${id}`)).toBeNull();
    expect(parseArtifactUrl(`/artifacts/${id}0`)).toBeNull();
  });
});
