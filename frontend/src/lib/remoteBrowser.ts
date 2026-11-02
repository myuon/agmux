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

/** Add a scheme to what the user typed in the URL bar. */
export function normalizeUrl(input: string): string {
  const v = input.trim();
  if (v === "") return "";
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) || /^(about|data|file):/i.test(v)) return v;
  if (/^(localhost|127\.|\[::1\]|\d+\.\d+\.\d+\.\d+)/i.test(v)) return `http://${v}`;
  return `https://${v}`;
}
