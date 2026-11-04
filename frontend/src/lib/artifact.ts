/**
 * URL of the raw (sandboxed) HTML of an artifact. `updatedAt` is appended as a
 * cache-busting query param: changing it changes the iframe `src`, which makes
 * the browser load the new content. The server ignores the param.
 */
export function artifactRawUrl(id: string, updatedAt?: string): string {
  const base = `/artifacts/${id}/raw`;
  return updatedAt ? `${base}?v=${encodeURIComponent(updatedAt)}` : base;
}
