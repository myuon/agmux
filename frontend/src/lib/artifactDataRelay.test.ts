import { describe, expect, it, vi } from "vitest";
import { createDataRelay, DATA_RESPONSE_TYPE, isValidDataName, MAX_IN_FLIGHT } from "./artifactDataRelay";

const artifactId = "e56d11d264cb2ac1987978e3b830968c";

function fakeWindow() {
  return { postMessage: vi.fn() };
}

function response(body: string, status = 200, contentType = "text/plain; charset=utf-8") {
  return new Response(body, { status, headers: { "Content-Type": contentType } });
}

function setup(fetchImpl: typeof fetch = vi.fn(async () => response("# ideas"))) {
  const frame = fakeWindow();
  const fetchFn = vi.fn(fetchImpl);
  const relay = createDataRelay({ artifactId, getFrameWindow: () => frame, fetch: fetchFn as unknown as typeof fetch });
  return { frame, fetchFn, relay };
}

const req = (name: unknown, reqId: unknown = "r1", type: unknown = "agmux:data-request") => ({ type, reqId, name });

describe("createDataRelay", () => {
  it("fetches same-origin and replies to its own iframe", async () => {
    const { frame, fetchFn, relay } = setup();
    await relay({ source: frame, data: req("ideas.md") });
    expect(fetchFn).toHaveBeenCalledWith(`/artifacts/${artifactId}/data/ideas.md`, { credentials: "same-origin" });
    expect(frame.postMessage).toHaveBeenCalledWith(
      {
        type: DATA_RESPONSE_TYPE,
        reqId: "r1",
        ok: true,
        status: 200,
        contentType: "text/plain; charset=utf-8",
        text: "# ideas",
      },
      "*",
    );
  });

  it("ignores messages from other windows", async () => {
    const { frame, fetchFn, relay } = setup();
    const other = fakeWindow();
    expect(relay({ source: other, data: req("ideas.md") })).toBeUndefined();
    expect(relay({ source: null, data: req("ideas.md") })).toBeUndefined();
    expect(relay({ source: globalThis, data: req("ideas.md") })).toBeUndefined();
    expect(fetchFn).not.toHaveBeenCalled();
    expect(other.postMessage).not.toHaveBeenCalled();
    expect(frame.postMessage).not.toHaveBeenCalled();
  });

  it("ignores everything when the iframe is not mounted", () => {
    const fetchFn = vi.fn();
    const relay = createDataRelay({ artifactId, getFrameWindow: () => null, fetch: fetchFn });
    expect(relay({ source: null, data: req("ideas.md") })).toBeUndefined();
    expect(fetchFn).not.toHaveBeenCalled();
  });

  it("ignores wrong types and malformed requests", () => {
    const { frame, fetchFn, relay } = setup();
    for (const data of [
      req("ideas.md", "r1", "agmux:data-response"),
      req("ideas.md", "r1", "agmux:data-request "),
      req("ideas.md", 1),
      req("ideas.md", ""),
      "agmux:data-request",
      null,
      undefined,
    ]) {
      expect(relay({ source: frame, data })).toBeUndefined();
    }
    expect(fetchFn).not.toHaveBeenCalled();
  });

  it("ignores invalid names", () => {
    const { frame, fetchFn, relay } = setup();
    for (const name of ["../x", ".env", "a/b", "a\\b", "", "a b", "%2e%2e", 42, null]) {
      expect(relay({ source: frame, data: req(name) })).toBeUndefined();
    }
    expect(fetchFn).not.toHaveBeenCalled();
    expect(frame.postMessage).not.toHaveBeenCalled();
  });

  it("passes through error statuses from the server", async () => {
    const { frame, relay } = setup(async () => response("404 page not found\n", 404));
    await relay({ source: frame, data: req("missing.json", "r9") });
    expect(frame.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({ type: DATA_RESPONSE_TYPE, reqId: "r9", ok: false, status: 404, text: "404 page not found\n" }),
      "*",
    );
  });

  it("reports network errors as status 0", async () => {
    const { frame, relay } = setup(async () => {
      throw new TypeError("Failed to fetch");
    });
    await relay({ source: frame, data: req("ideas.md") });
    expect(frame.postMessage).toHaveBeenCalledWith(
      { type: DATA_RESPONSE_TYPE, reqId: "r1", ok: false, status: 0, contentType: "", text: "", error: "Failed to fetch" },
      "*",
    );
  });

  it("caps requests in flight", async () => {
    const resolvers: Array<() => void> = [];
    const { frame, fetchFn, relay } = setup(
      () => new Promise<Response>((resolve) => resolvers.push(() => resolve(response("x")))),
    );
    const pending = [];
    for (let i = 0; i < MAX_IN_FLIGHT; i++) pending.push(relay({ source: frame, data: req("a.txt", `r${i}`) }));
    expect(relay({ source: frame, data: req("a.txt", "over") })).toBeUndefined();
    expect(fetchFn).toHaveBeenCalledTimes(MAX_IN_FLIGHT);
    resolvers.forEach((r) => r());
    await Promise.all(pending);
    expect(frame.postMessage).toHaveBeenCalledTimes(MAX_IN_FLIGHT);
    // Slots free up after completion.
    resolvers.length = 0;
    const p = relay({ source: frame, data: req("a.txt", "again") });
    expect(p).toBeDefined();
    resolvers.forEach((r) => r());
    await p;
  });
});

describe("isValidDataName", () => {
  it("matches the server's rules", () => {
    expect(isValidDataName("ideas.json")).toBe(true);
    expect(isValidDataName("A-b_c.1.csv")).toBe(true);
    expect(isValidDataName(".hidden")).toBe(false);
    expect(isValidDataName("a/b")).toBe(false);
  });
});
