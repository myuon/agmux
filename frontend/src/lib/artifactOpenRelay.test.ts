import { describe, expect, it, vi } from "vitest";
import { createOpenArtifactHandler, isValidArtifactId } from "./artifactOpenRelay";

const current = "e56d11d264cb2ac1987978e3b830968c";
const target = "0123456789abcdef0123456789abcdef";

function setup(exists: (id: string) => Promise<unknown> = async () => ({})) {
  const frame = { postMessage: vi.fn() };
  const existsFn = vi.fn(exists);
  const navigate = vi.fn();
  const handle = createOpenArtifactHandler({ currentId: current, getFrameWindow: () => frame, exists: existsFn, navigate });
  return { frame, existsFn, navigate, handle };
}

const msg = (id: unknown, type: unknown = "agmux:open-artifact") => ({ type, id });

describe("createOpenArtifactHandler", () => {
  it("navigates for a valid message from its own iframe", async () => {
    const { frame, existsFn, navigate, handle } = setup();
    await handle({ source: frame, data: msg(target) });
    expect(existsFn).toHaveBeenCalledWith(target);
    expect(navigate).toHaveBeenCalledWith(`/artifacts/${target}`);
  });

  it("ignores other sources", () => {
    const { existsFn, navigate, handle } = setup();
    expect(handle({ source: { postMessage: vi.fn() }, data: msg(target) })).toBeUndefined();
    expect(handle({ source: null, data: msg(target) })).toBeUndefined();
    expect(existsFn).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("ignores everything when the iframe is not mounted", () => {
    const exists = vi.fn();
    const navigate = vi.fn();
    const handle = createOpenArtifactHandler({ currentId: current, getFrameWindow: () => null, exists, navigate });
    expect(handle({ source: null, data: msg(target) })).toBeUndefined();
    expect(exists).not.toHaveBeenCalled();
  });

  it("ignores wrong types and malformed ids", () => {
    const { frame, existsFn, navigate, handle } = setup();
    for (const data of [
      msg(target, "agmux:data-request"),
      msg(undefined),
      msg(123),
      msg(""),
      msg(target.toUpperCase()),
      msg(target + "0"),
      msg("../sessions"),
      msg(`${target}/../x`),
      msg("https://example.com"),
      null,
      "agmux:open-artifact",
    ]) {
      expect(handle({ source: frame, data })).toBeUndefined();
    }
    expect(existsFn).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("is a no-op for the current artifact", () => {
    const { frame, existsFn, navigate, handle } = setup();
    expect(handle({ source: frame, data: msg(current) })).toBeUndefined();
    expect(existsFn).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("does not navigate to a nonexistent artifact", async () => {
    const { frame, navigate, handle } = setup(async () => {
      throw new Error("404");
    });
    await handle({ source: frame, data: msg(target) });
    expect(navigate).not.toHaveBeenCalled();
  });

  it("does not navigate if the iframe changed meanwhile", async () => {
    let win: unknown = { postMessage: vi.fn() };
    const frame = win;
    const navigate = vi.fn();
    const handle = createOpenArtifactHandler({
      currentId: current,
      getFrameWindow: () => win,
      exists: async () => {
        win = null;
      },
      navigate,
    });
    await handle({ source: frame, data: msg(target) });
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("isValidArtifactId", () => {
  it("accepts only 32 lowercase hex chars", () => {
    expect(isValidArtifactId(target)).toBe(true);
    expect(isValidArtifactId("xyz")).toBe(false);
  });
});
