import { useEffect, type RefObject } from "react";
import { createDataRelay } from "../lib/artifactDataRelay";

// useArtifactDataRelay answers agmuxData() requests from the artifact iframe
// (see lib/artifactDataRelay.ts). Use it wherever an artifact iframe is
// rendered.
export function useArtifactDataRelay(iframeRef: RefObject<HTMLIFrameElement | null>, artifactId: string | undefined) {
  useEffect(() => {
    if (!artifactId) return;
    const relay = createDataRelay({
      artifactId,
      getFrameWindow: () => iframeRef.current?.contentWindow ?? null,
    });
    const onMessage = (e: MessageEvent) => {
      void relay(e);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [iframeRef, artifactId]);
}
