// Pure helpers for the remote browser screen.

export interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface Point {
  x: number;
  y: number;
}

/**
 * Map a client (viewport) coordinate to a CSS-pixel coordinate of the remote
 * page. The frame is drawn with `object-fit: contain` inside `rect`, so the
 * image may be letterboxed. Returns null when the point lies outside the
 * drawn image (or sizes are not yet known).
 *
 * cssX = imgX * deviceWidth / imageWidth, where imgX is the position inside
 * the image in image pixels (frames are typically 2x the CSS size).
 */
export function mapClientToPage(
  clientX: number,
  clientY: number,
  rect: Rect,
  imageWidth: number,
  imageHeight: number,
  deviceWidth: number,
  deviceHeight: number,
): Point | null {
  if (imageWidth <= 0 || imageHeight <= 0 || deviceWidth <= 0 || deviceHeight <= 0) return null;
  if (rect.width <= 0 || rect.height <= 0) return null;
  const scale = Math.min(rect.width / imageWidth, rect.height / imageHeight);
  const drawnW = imageWidth * scale;
  const drawnH = imageHeight * scale;
  const offX = rect.left + (rect.width - drawnW) / 2;
  const offY = rect.top + (rect.height - drawnH) / 2;
  const imgX = (clientX - offX) / scale;
  const imgY = (clientY - offY) / scale;
  if (imgX < 0 || imgY < 0 || imgX > imageWidth || imgY > imageHeight) return null;
  return {
    x: (imgX * deviceWidth) / imageWidth,
    y: (imgY * deviceHeight) / imageHeight,
  };
}

/**
 * Convert a drag distance on screen (client px) to a CSS-pixel scroll delta of
 * the remote page, given the same contain-fit layout.
 */
export function screenDeltaToPage(
  dx: number,
  dy: number,
  rect: Rect,
  imageWidth: number,
  imageHeight: number,
  deviceWidth: number,
  deviceHeight: number,
): Point {
  if (imageWidth <= 0 || imageHeight <= 0 || deviceWidth <= 0 || deviceHeight <= 0) return { x: 0, y: 0 };
  const scale = Math.min(rect.width / imageWidth, rect.height / imageHeight);
  if (!(scale > 0)) return { x: 0, y: 0 };
  return {
    x: (dx / scale) * (deviceWidth / imageWidth),
    y: (dy / scale) * (deviceHeight / imageHeight),
  };
}

/** Values the agent's browser-login request passes to the remote browser page. */
export interface LoginRequestParams {
  url: string;
  host: string;
  escalationId: string;
  sessionId: string;
}

/** Only absolute http/https URLs with a host are allowed. Returns the host or null. */
export function loginUrlHost(raw: string): string | null {
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return null;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  if (!u.hostname) return null;
  return u.host;
}

/**
 * Parse `?url=&escalation=&session=` of /remote-browser. Returns null when
 * any is missing or the url is not a plain http(s) URL.
 */
export function parseLoginRequestParams(search: string): LoginRequestParams | null {
  const p = new URLSearchParams(search);
  const url = p.get("url")?.trim() ?? "";
  const escalationId = p.get("escalation") ?? "";
  const sessionId = p.get("session") ?? "";
  if (!url || !escalationId || !sessionId) return null;
  const host = loginUrlHost(url);
  if (!host) return null;
  return { url, host, escalationId, sessionId };
}

/** Path the notification / banner opens for a browser-login request. */
export function loginRequestPath(url: string, escalationId: string, sessionId: string): string {
  const q = new URLSearchParams({ url, escalation: escalationId, session: sessionId });
  return `/remote-browser?${q.toString()}`;
}

/** Add a scheme to what the user typed in the URL bar. */
export function normalizeUrl(input: string): string {
  const v = input.trim();
  if (v === "") return "";
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) || /^(about|data|file):/i.test(v)) return v;
  if (/^(localhost|127\.|\[::1\]|\d+\.\d+\.\d+\.\d+)/i.test(v)) return `http://${v}`;
  return `https://${v}`;
}

export interface FocusState {
  secure: boolean;
  editable: boolean;
}

/** Parses a server "focus" message; returns null for anything else. */
export function parseFocusMessage(msg: unknown): FocusState | null {
  if (!msg || typeof msg !== "object") return null;
  const m = msg as Record<string, unknown>;
  if (m.type !== "focus") return null;
  return { secure: m.secure === true, editable: m.editable === true };
}

/**
 * Password mode is on when the user turned it on manually (stays on until
 * toggled off, regardless of focus messages) or the page focused field is
 * detected as a password field.
 */
export function isSecureInput(manual: boolean, detected: boolean): boolean {
  return manual || detected;
}

/** Type of the hidden text sink; "password" stops mobile keyboards from learning. */
export function sinkInputType(secure: boolean): "text" | "password" {
  return secure ? "password" : "text";
}

export type ViewportMode = "desktop" | "mobile";

export const VIEWPORT_STORAGE_KEY = "agmux.remoteBrowser.viewport";
/** Clients narrower than this default to the mobile viewport. */
export const MOBILE_BREAKPOINT_PX = 768;
/** CSS width of the emulated mobile viewport (must match the server). */
export const MOBILE_VIEWPORT_WIDTH = 390;

/** The saved choice wins; otherwise narrow clients start in mobile mode. */
export function initialViewportMode(saved: string | null, clientWidth: number): ViewportMode {
  if (saved === "desktop" || saved === "mobile") return saved;
  return clientWidth < MOBILE_BREAKPOINT_PX ? "mobile" : "desktop";
}

/**
 * CSS height to request for the mobile viewport so that a 390px-wide frame
 * fills the frame area (areaWidth x areaHeight on screen) without
 * letterboxing. Returns 0 (= server default) before the area is laid out.
 * The server clamps the value.
 */
export function mobileViewportHeight(areaWidth: number, areaHeight: number): number {
  if (!(areaWidth > 0) || !(areaHeight > 0)) return 0;
  return Math.round((MOBILE_VIEWPORT_WIDTH * areaHeight) / areaWidth);
}

// ---- client-side zoom of the displayed frame ----

/**
 * Zoom of the frame image, applied as `translate(tx, ty) scale(scale)` with
 * transform-origin 0 0. Coordinates are local to the frame area (the image
 * element fills the area at 1x).
 */
export interface ZoomState {
  scale: number;
  tx: number;
  ty: number;
}

export const MIN_ZOOM = 1;
export const MAX_ZOOM = 5;
export const IDENTITY_ZOOM: ZoomState = { scale: 1, tx: 0, ty: 0 };

export interface Size {
  width: number;
  height: number;
}

export function isZoomed(z: ZoomState): boolean {
  return z.scale > MIN_ZOOM + 1e-6;
}

/**
 * Where an `object-fit: contain` image of imageWidth x imageHeight is drawn
 * inside an area (local coordinates, untransformed). Falls back to the whole
 * area when the image size is not known yet.
 */
export function containRect(area: Size, imageWidth: number, imageHeight: number): Rect {
  if (!(imageWidth > 0) || !(imageHeight > 0) || !(area.width > 0) || !(area.height > 0)) {
    return { left: 0, top: 0, width: Math.max(area.width, 0), height: Math.max(area.height, 0) };
  }
  const s = Math.min(area.width / imageWidth, area.height / imageHeight);
  const width = imageWidth * s;
  const height = imageHeight * s;
  return { left: (area.width - width) / 2, top: (area.height - height) / 2, width, height };
}

function clampAxis(t: number, scale: number, areaLen: number, start: number, len: number): number {
  const drawn = len * scale;
  // Content smaller than the area on this axis: keep it centered.
  if (drawn <= areaLen) return (areaLen - drawn) / 2 - start * scale;
  const min = areaLen - (start + len) * scale; // right/bottom edge on the area's edge
  const max = 0 - start * scale; // left/top edge on the area's edge
  return Math.min(max, Math.max(min, t));
}

/**
 * Clamp the scale to [MIN_ZOOM, MAX_ZOOM] and the pan so the drawn content
 * (`content`, local untransformed rect) always covers the area on axes where
 * it is larger than the area, and stays centered otherwise. At 1x this always
 * yields the identity transform.
 */
export function clampZoom(z: ZoomState, area: Size, content: Rect): ZoomState {
  const scale = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Number.isFinite(z.scale) ? z.scale : MIN_ZOOM));
  if (scale <= MIN_ZOOM) return { ...IDENTITY_ZOOM };
  return {
    scale,
    tx: clampAxis(z.tx, scale, area.width, content.left, content.width),
    ty: clampAxis(z.ty, scale, area.height, content.top, content.height),
  };
}

/**
 * Change the scale to `scale` keeping the content point under `anchor`
 * (local coordinates) fixed, then clamp.
 */
export function zoomAt(z: ZoomState, anchor: Point, scale: number, area: Size, content: Rect): ZoomState {
  const px = (anchor.x - z.tx) / z.scale;
  const py = (anchor.y - z.ty) / z.scale;
  const s = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, scale));
  return clampZoom({ scale: s, tx: anchor.x - px * s, ty: anchor.y - py * s }, area, content);
}

/**
 * Two-finger pinch: from the zoom at pinch start (`start`, with the fingers'
 * midpoint `startMid` and distance `startDist`) to the current midpoint and
 * distance. The content point under the start midpoint follows the current
 * midpoint, so moving both fingers also pans.
 */
export function pinchZoom(
  start: ZoomState,
  startMid: Point,
  startDist: number,
  mid: Point,
  dist: number,
  area: Size,
  content: Rect,
): ZoomState {
  const ratio = startDist > 0 && dist > 0 ? dist / startDist : 1;
  const s = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, start.scale * ratio));
  const px = (startMid.x - start.tx) / start.scale;
  const py = (startMid.y - start.ty) / start.scale;
  return clampZoom({ scale: s, tx: mid.x - px * s, ty: mid.y - py * s }, area, content);
}

/**
 * One-finger drag of (dx, dy) screen px while zoomed: pan the view as far as
 * the clamp allows; the remainder (what could not be absorbed because the
 * view is at its edge) is returned for scrolling the remote page. At 1x the
 * whole delta is the remainder.
 */
export function panZoom(
  z: ZoomState,
  dx: number,
  dy: number,
  area: Size,
  content: Rect,
): { zoom: ZoomState; restX: number; restY: number } {
  if (!isZoomed(z)) return { zoom: z, restX: dx, restY: dy };
  const next = clampZoom({ scale: z.scale, tx: z.tx + dx, ty: z.ty + dy }, area, content);
  return { zoom: next, restX: dx - (next.tx - z.tx), restY: dy - (next.ty - z.ty) };
}

/** Client rect of the zoomed image element, given the untransformed area rect. */
export function zoomedRect(area: Rect, z: ZoomState): Rect {
  return {
    left: area.left + z.tx,
    top: area.top + z.ty,
    width: area.width * z.scale,
    height: area.height * z.scale,
  };
}
