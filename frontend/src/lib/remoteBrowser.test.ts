import { describe, expect, it } from "vitest";
import {
  clampZoom,
  containRect,
  IDENTITY_ZOOM,
  initialViewportMode,
  isZoomed,
  MAX_ZOOM,
  panZoom,
  pinchZoom,
  zoomAt,
  zoomedRect,
  isSecureInput,
  loginRequestPath,
  mobileViewportHeight,
  loginUrlHost,
  mapClientToPage,
  normalizeUrl,
  parseFocusMessage,
  parseLoginRequestParams,
  screenDeltaToPage,
  sinkInputType,
} from "./remoteBrowser";

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

describe("login request params", () => {
  it("round-trips through loginRequestPath", () => {
    const path = loginRequestPath("https://a.test/login?next=/x&y=1", "e1", "s1");
    expect(parseLoginRequestParams(path.split("?")[1])).toEqual({
      url: "https://a.test/login?next=/x&y=1",
      host: "a.test",
      escalationId: "e1",
      sessionId: "s1",
    });
  });

  it("rejects non-http(s) schemes and missing params", () => {
    expect(loginUrlHost("javascript:alert(1)")).toBeNull();
    expect(loginUrlHost("file:///etc/passwd")).toBeNull();
    expect(loginUrlHost("//a.test")).toBeNull();
    expect(loginUrlHost("http://localhost:8080/x")).toBe("localhost:8080");
    expect(parseLoginRequestParams("url=javascript:1&escalation=e&session=s")).toBeNull();
    expect(parseLoginRequestParams("url=https://a.test&session=s")).toBeNull();
    expect(parseLoginRequestParams("")).toBeNull();
  });
});

describe("password mode helpers", () => {
  it("parses focus messages", () => {
    expect(parseFocusMessage({ type: "focus", secure: true, editable: true })).toEqual({ secure: true, editable: true });
    expect(parseFocusMessage({ type: "focus", secure: "yes" })).toEqual({ secure: false, editable: false });
    expect(parseFocusMessage({ type: "frame" })).toBeNull();
    expect(parseFocusMessage(null)).toBeNull();
    expect(parseFocusMessage("focus")).toBeNull();
  });

  it("manual mode overrides detection", () => {
    expect(isSecureInput(true, false)).toBe(true);
    expect(isSecureInput(false, true)).toBe(true);
    expect(isSecureInput(false, false)).toBe(false);
  });

  it("chooses the sink type", () => {
    expect(sinkInputType(true)).toBe("password");
    expect(sinkInputType(false)).toBe("text");
  });
});

describe("initialViewportMode", () => {
  it("uses the saved choice", () => {
    expect(initialViewportMode("desktop", 375)).toBe("desktop");
    expect(initialViewportMode("mobile", 1440)).toBe("mobile");
  });
  it("falls back to the client width", () => {
    expect(initialViewportMode(null, 375)).toBe("mobile");
    expect(initialViewportMode(null, 767)).toBe("mobile");
    expect(initialViewportMode(null, 768)).toBe("desktop");
    expect(initialViewportMode("bogus", 1440)).toBe("desktop");
  });
});

describe("mobileViewportHeight", () => {
  it("matches the frame area's aspect ratio", () => {
    expect(mobileViewportHeight(390, 650)).toBe(650);
    expect(mobileViewportHeight(195, 400)).toBe(800);
  });
  it("returns 0 before layout", () => {
    expect(mobileViewportHeight(0, 600)).toBe(0);
    expect(mobileViewportHeight(390, 0)).toBe(0);
  });
});

describe("zoom helpers", () => {
  const area = { width: 400, height: 800 };
  const full = { left: 0, top: 0, width: 400, height: 800 };
  // Wide (PC) frame letterboxed vertically: drawn 400x200 at top=300.
  const wide = containRect(area, 1600, 800);

  it("computes the contain rect", () => {
    expect(wide).toEqual({ left: 0, top: 300, width: 400, height: 200 });
    expect(containRect(area, 0, 0)).toEqual(full);
  });

  it("clamps scale and returns identity at 1x", () => {
    expect(clampZoom({ scale: 0.5, tx: 30, ty: -10 }, area, full)).toEqual(IDENTITY_ZOOM);
    expect(clampZoom({ scale: 9, tx: 0, ty: 0 }, area, full).scale).toBe(MAX_ZOOM);
    expect(isZoomed(IDENTITY_ZOOM)).toBe(false);
    expect(isZoomed({ scale: 2, tx: 0, ty: 0 })).toBe(true);
  });

  it("keeps the content covering the area", () => {
    // 2x of a 400x800 area: tx in [-400, 0], ty in [-800, 0]
    expect(clampZoom({ scale: 2, tx: 50, ty: -2000 }, area, full)).toEqual({ scale: 2, tx: 0, ty: -800 });
    expect(clampZoom({ scale: 2, tx: -500, ty: 10 }, area, full)).toEqual({ scale: 2, tx: -400, ty: 0 });
  });

  it("centers an axis where the content is smaller than the area", () => {
    // wide frame at 2x: drawn 800x400 -> vertically centered (400 < 800)
    const z = clampZoom({ scale: 2, tx: 0, ty: 0 }, area, wide);
    expect(z.ty).toBe(200 - 600); // (800-400)/2 - 300*2
    // at 3x: drawn height 600 still < 800; at 5x: 1000 > 800 -> clamped to content edges
    const z5 = clampZoom({ scale: 5, tx: 0, ty: 0 }, area, wide);
    expect(z5.ty).toBe(-1500); // top edge of the content on the area top
    expect(clampZoom({ scale: 5, tx: 0, ty: -99999 }, area, wide).ty).toBe(800 - 500 * 5);
  });

  it("zooms around an anchor", () => {
    const z = zoomAt(IDENTITY_ZOOM, { x: 200, y: 400 }, 2, area, full);
    expect(z).toEqual({ scale: 2, tx: -200, ty: -400 });
    // the point under the anchor stays put
    expect((200 - z.tx) / z.scale).toBe(200);
  });

  it("pinch zooms at the midpoint and pans with it", () => {
    const z = pinchZoom(IDENTITY_ZOOM, { x: 100, y: 100 }, 100, { x: 100, y: 100 }, 200, area, full);
    expect(z).toEqual({ scale: 2, tx: -100, ty: -100 });
    // moving both fingers by (-50, -30) at the same distance pans by that much
    const moved = pinchZoom(z, { x: 100, y: 100 }, 100, { x: 50, y: 70 }, 100, area, full);
    expect(moved).toEqual({ scale: 2, tx: -150, ty: -130 });
    // pinching in past 1x clamps to identity
    expect(pinchZoom(z, { x: 0, y: 0 }, 200, { x: 0, y: 0 }, 10, area, full)).toEqual(IDENTITY_ZOOM);
  });

  it("splits a drag into pan and remainder", () => {
    const z = { scale: 2, tx: -350, ty: 0 };
    // can move 350 right; nothing up (already at the top)
    const r = panZoom(z, 400, 30, area, full);
    expect(r.zoom).toEqual({ scale: 2, tx: 0, ty: 0 });
    expect(r.restX).toBe(50);
    expect(r.restY).toBe(30);
    const r2 = panZoom(z, -20, -30, area, full);
    expect(r2.zoom).toEqual({ scale: 2, tx: -370, ty: -30 });
    expect(r2.restX).toBe(0);
    expect(r2.restY).toBe(0);
  });

  it("passes the whole drag through at 1x", () => {
    expect(panZoom(IDENTITY_ZOOM, 12, -7, area, full)).toEqual({ zoom: IDENTITY_ZOOM, restX: 12, restY: -7 });
  });

  it("maps taps correctly through the zoomed, letterboxed rect", () => {
    // wide 1600x800 image of a 800x400 CSS page in a 400x800 area at (10, 50)
    const areaRect = { left: 10, top: 50, width: 400, height: 800 };
    const z = zoomAt(IDENTITY_ZOOM, { x: 100, y: 350 }, 3, area, wide);
    const rect = zoomedRect(areaRect, z);
    // the page point (200, 100) is drawn at local (100, 350) at 1x
    const atOne = mapClientToPage(110, 400, areaRect, 1600, 800, 800, 400);
    expect(atOne).toEqual({ x: 200, y: 100 });
    // after zooming, it is drawn at local t + s*p
    const cx = areaRect.left + z.tx + 100 * z.scale;
    const cy = areaRect.top + z.ty + 350 * z.scale;
    const at = mapClientToPage(cx, cy, rect, 1600, 800, 800, 400)!;
    expect(at.x).toBeCloseTo(200);
    expect(at.y).toBeCloseTo(100);
    // a drag of 30 screen px at 3x scrolls a third as far as at 1x
    expect(screenDeltaToPage(30, 0, rect, 1600, 800, 800, 400).x).toBeCloseTo(
      screenDeltaToPage(30, 0, areaRect, 1600, 800, 800, 400).x / 3,
    );
  });
});
