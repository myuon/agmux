// Parent-side relay for artifact data files.
//
// Artifacts run in a sandboxed iframe with an opaque origin, so behind an
// authenticating proxy (Cloudflare Access) their own fetch of
// /artifacts/<id>/data/<name> is cross-site, goes without the auth cookie and
// fails. The helper the server injects into such artifacts (window.agmuxData)
// asks this page instead:
//
//   iframe → parent: {type: "agmux:data-request", reqId, name}
//   parent → iframe: {type: "agmux:data-response", reqId, ok, status, contentType, text, error?}
//
// Only messages whose source is the artifact iframe's own window are answered,
// and only for that artifact's data URL; the server still enforces the
// per-artifact whitelist.

export const DATA_REQUEST_TYPE = "agmux:data-request";
export const DATA_RESPONSE_TYPE = "agmux:data-response";
export const MAX_IN_FLIGHT = 32;

const dataNamePattern = /^[A-Za-z0-9._-]+$/;

export function isValidDataName(name: unknown): name is string {
  return typeof name === "string" && name.length <= 255 && dataNamePattern.test(name) && !name.startsWith(".");
}

export interface DataResponse {
  type: typeof DATA_RESPONSE_TYPE;
  reqId: string;
  ok: boolean;
  status: number;
  contentType: string;
  text: string;
  error?: string;
}

interface PostTarget {
  postMessage(message: unknown, targetOrigin: string): void;
}

export interface RelayMessageEvent {
  source: unknown;
  data: unknown;
}

export interface DataRelayOptions {
  artifactId: string;
  // Returns the iframe's current contentWindow (null when not mounted).
  getFrameWindow: () => unknown;
  fetch?: typeof fetch;
}

// createDataRelay returns a `message` event handler implementing the relay.
export function createDataRelay({ artifactId, getFrameWindow, fetch: fetchFn = fetch }: DataRelayOptions) {
  let inFlight = 0;

  const handler = (event: RelayMessageEvent): Promise<void> | undefined => {
    const frame = getFrameWindow();
    if (!frame || event.source !== frame) return;
    const d = event.data as { type?: unknown; reqId?: unknown; name?: unknown } | null;
    if (!d || typeof d !== "object" || d.type !== DATA_REQUEST_TYPE) return;
    if (typeof d.reqId !== "string" || d.reqId.length === 0 || d.reqId.length > 128) return;
    if (!isValidDataName(d.name)) return;
    if (inFlight >= MAX_IN_FLIGHT) return;

    const target = event.source as PostTarget;
    const reqId = d.reqId;
    const url = `/artifacts/${encodeURIComponent(artifactId)}/data/${encodeURIComponent(d.name)}`;
    inFlight++;
    const reply = (r: Omit<DataResponse, "type" | "reqId">) => {
      try {
        // The iframe's origin is opaque, so "*" is the only usable
        // targetOrigin; posting to that specific window is what scopes it.
        target.postMessage({ type: DATA_RESPONSE_TYPE, reqId, ...r }, "*");
      } catch {
        // The frame went away.
      }
    };
    return fetchFn(url, { credentials: "same-origin" })
      .then(async (res) => {
        const text = await res.text();
        reply({ ok: res.ok, status: res.status, contentType: res.headers.get("Content-Type") ?? "", text });
      })
      .catch((err: unknown) => {
        reply({ ok: false, status: 0, contentType: "", text: "", error: err instanceof Error ? err.message : String(err) });
      })
      .finally(() => {
        inFlight--;
      });
  };
  return handler;
}
