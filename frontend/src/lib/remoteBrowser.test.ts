import { describe, expect, it } from "vitest";
import { mapClientToPage, normalizeUrl, screenDeltaToPage } from "./remoteBrowser";

describe("mapClientToPage", () => {
  // 2x frame of a 400x800 CSS page => 800x1600 image
  const img = { w: 800, h: 1600, dw: 400, dh: 800 };

  it("maps without letterboxing", () => {
    const rect = { left: 0, top: 0, width: 400, height: 800 };
    expect(mapClientToPage(100, 200, rect, img.w, img.h, img.dw, img.dh)).toEqual({ x: 100, y: 200 });
  });

  it("accounts for element offset and scaling", () => {
    const rect = { left: 10, top: 50, width: 200, height: 400 };
    expect(mapClientToPage(110, 250, rect, img.w, img.h, img.dw, img.dh)).toEqual({ x: 200, y: 400 });
  });

  it("accounts for horizontal letterboxing", () => {
    // wider box: image drawn 200x400 centered => 100px bars each side
    const rect = { left: 0, top: 0, width: 400, height: 400 };
    expect(mapClientToPage(100, 0, rect, img.w, img.h, img.dw, img.dh)).toEqual({ x: 0, y: 0 });
    expect(mapClientToPage(200, 200, rect, img.w, img.h, img.dw, img.dh)).toEqual({ x: 200, y: 400 });
    expect(mapClientToPage(50, 200, rect, img.w, img.h, img.dw, img.dh)).toBeNull();
  });

  it("accounts for vertical letterboxing", () => {
    const rect = { left: 0, top: 0, width: 200, height: 600 };
    // drawn 200x400, top bar 100
    expect(mapClientToPage(0, 100, rect, img.w, img.h, img.dw, img.dh)).toEqual({ x: 0, y: 0 });
    expect(mapClientToPage(0, 50, rect, img.w, img.h, img.dw, img.dh)).toBeNull();
  });

  it("returns null for unknown sizes", () => {
    expect(mapClientToPage(1, 1, { left: 0, top: 0, width: 10, height: 10 }, 0, 0, 0, 0)).toBeNull();
  });
});

describe("screenDeltaToPage", () => {
  it("scales drag distance", () => {
    const rect = { left: 0, top: 0, width: 200, height: 400 };
    expect(screenDeltaToPage(10, -20, rect, 800, 1600, 400, 800)).toEqual({ x: 20, y: -40 });
  });
});

describe("normalizeUrl", () => {
  it("adds schemes", () => {
    expect(normalizeUrl("example.com")).toBe("https://example.com");
    expect(normalizeUrl("localhost:8000/x")).toBe("http://localhost:8000/x");
    expect(normalizeUrl("http://a.test")).toBe("http://a.test");
    expect(normalizeUrl("  ")).toBe("");
  });
});
