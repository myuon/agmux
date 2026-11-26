import { useEffect, useRef, useSyncExternalStore } from "react";

export function isPageHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

function subscribe(cb: () => void) {
  document.addEventListener("visibilitychange", cb);
  return () => document.removeEventListener("visibilitychange", cb);
}

/** True while the tab is visible. */
export function usePageVisible(): boolean {
  return useSyncExternalStore(subscribe, () => !isPageHidden(), () => true);
}

/**
 * setInterval that pauses while the tab is hidden (saves mobile data/battery).
 * When the tab becomes visible again the callback runs once immediately and
 * the interval restarts. It does NOT run at start; callers keep their own
 * initial load. Returns a cleanup function.
 */
export function visibleInterval(callback: () => void, ms: number): () => void {
  let timer: ReturnType<typeof setInterval> | undefined;
  const start = () => {
    if (timer === undefined) timer = setInterval(callback, ms);
  };
  const stop = () => {
    if (timer !== undefined) clearInterval(timer);
    timer = undefined;
  };
  const onVisibility = () => {
    stop();
    if (!isPageHidden()) {
      callback();
      start();
    }
  };
  if (!isPageHidden()) start();
  document.addEventListener("visibilitychange", onVisibility);
  return () => {
    document.removeEventListener("visibilitychange", onVisibility);
    stop();
  };
}

/** Hook form of visibleInterval; the latest callback is always used. */
export function useVisibleInterval(callback: () => void, ms: number, enabled = true) {
  const cbRef = useRef(callback);
  useEffect(() => {
    cbRef.current = callback;
  });
  useEffect(() => {
    if (!enabled) return;
    return visibleInterval(() => cbRef.current(), ms);
  }, [ms, enabled]);
}
