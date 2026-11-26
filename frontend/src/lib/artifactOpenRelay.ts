// Parent-side handler for "open another artifact" requests from an artifact.
//
//   iframe → parent: {type: "agmux:open-artifact", id}
//
// Only messages whose source is the artifact iframe's own window are
// accepted, the id must have the shape of a generated artifact id and refer to
// an existing artifact, and the only place we ever navigate to is
// /artifacts/<id>.

export const OPEN_ARTIFACT_TYPE = "agmux:open-artifact";

const artifactIdPattern = /^[0-9a-f]{32}$/;

export function isValidArtifactId(id: unknown): id is string {
  return typeof id === "string" && artifactIdPattern.test(id);
}

export interface OpenRelayMessageEvent {
  source: unknown;
  data: unknown;
}

export interface OpenRelayOptions {
  currentId: string;
  // Returns the iframe's current contentWindow (null when not mounted).
  getFrameWindow: () => unknown;
  // Resolves when the artifact exists; rejects (e.g. 404) otherwise.
  exists: (id: string) => Promise<unknown>;
  navigate: (path: string) => void;
}

export function createOpenArtifactHandler({ currentId, getFrameWindow, exists, navigate }: OpenRelayOptions) {
  return (event: OpenRelayMessageEvent): Promise<void> | undefined => {
    const frame = getFrameWindow();
    if (!frame || event.source !== frame) return;
    const d = event.data as { type?: unknown; id?: unknown } | null;
    if (!d || typeof d !== "object" || d.type !== OPEN_ARTIFACT_TYPE) return;
    const id = d.id;
    if (!isValidArtifactId(id) || id === currentId) return;
    return exists(id).then(
      () => {
        // The iframe may have been replaced while we were checking.
        if (getFrameWindow() !== frame) return;
        navigate(`/artifacts/${id}`);
      },
      () => {},
    );
  };
}
