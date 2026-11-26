import { useEffect, type RefObject } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { createDataRelay } from "../lib/artifactDataRelay";
import { createOpenArtifactHandler } from "../lib/artifactOpenRelay";

// useArtifactDataRelay answers agmuxData() requests from the artifact iframe
// (see lib/artifactDataRelay.ts) and open-artifact requests (see
// lib/artifactOpenRelay.ts). Use it wherever an artifact iframe is
// rendered.
export function useArtifactDataRelay(iframeRef: RefObject<HTMLIFrameElement | null>, artifactId: string | undefined) {
  const navigate = useNavigate();
  useEffect(() => {
    if (!artifactId) return;
    const relay = createDataRelay({
      artifactId,
      getFrameWindow: () => iframeRef.current?.contentWindow ?? null,
    });
    const open = createOpenArtifactHandler({
      currentId: artifactId,
      getFrameWindow: () => iframeRef.current?.contentWindow ?? null,
      exists: (id) => api.getArtifact(id),
      navigate: (path) => navigate(path),
    });
    const onMessage = (e: MessageEvent) => {
      void relay(e);
      void open(e);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [iframeRef, artifactId, navigate]);
}
