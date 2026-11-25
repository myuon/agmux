import type { Artifact } from "../api/client";

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

/** Pinned artifacts first, then most recently updated first. */
export function sortArtifacts(list: Artifact[]): Artifact[] {
  return [...list].sort((a, b) => {
    if (!!a.pinned !== !!b.pinned) return a.pinned ? -1 : 1;
    return Date.parse(b.updatedAt || b.createdAt) - Date.parse(a.updatedAt || a.createdAt);
  });
}

export const ARTIFACT_WS_TYPES = [
  "artifact_created",
  "artifact_updated",
  "artifact_deleted",
  "artifact_pinned",
];
