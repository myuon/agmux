import { useEffect, useState } from "react";
import { api } from "../api/client";

/**
 * Returns whether the named experimental feature is enabled, or null until the
 * first answer arrives (so callers can avoid drawing a layout that may flip).
 */
export function useExperimentalState(name: string): boolean | null {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .listExperimental()
        .then((r) => {
          if (!cancelled) setEnabled(r.features.some((f) => f.name === name && f.enabled));
        })
        .catch(() => {
          // Treat an unreachable flag API as "off" rather than waiting forever.
          if (!cancelled) setEnabled((v) => v ?? false);
        });
    load();
    window.addEventListener("agmux:experimental-changed", load);
    return () => {
      cancelled = true;
      window.removeEventListener("agmux:experimental-changed", load);
    };
  }, [name]);
  return enabled;
}

/** Returns whether the named experimental feature is enabled (false while loading). */
export function useExperimental(name: string): boolean {
  return useExperimentalState(name) === true;
}
