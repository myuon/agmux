/**
 * URL of the raw (sandboxed) HTML of an artifact. `updatedAt` is appended as a
 * cache-busting query param: changing it changes the iframe `src`, which makes
 * the browser load the new content. The server ignores the param.
 */
export function artifactRawUrl(id: string, updatedAt?: string): string {
  const base = `/artifacts/${id}/raw`;
  return updatedAt ? `${base}?v=${encodeURIComponent(updatedAt)}` : base;
}

const ARTIFACT_URL_RE = /^(?:https?:\/\/[^/\s?#]+)?\/artifacts\/([0-9a-f]{32})\/?$/;

/**
 * If `href` is an agmux artifact page URL (absolute with any host, or the bare
 * relative path `/artifacts/<id>`), returns the artifact id; otherwise null.
 * The raw URL (`/artifacts/<id>/raw`) is intentionally not matched.
 */
export function parseArtifactUrl(href: string | undefined | null): string | null {
  if (!href) return null;
  const m = ARTIFACT_URL_RE.exec(href.trim());
  return m ? m[1] : null;
}
