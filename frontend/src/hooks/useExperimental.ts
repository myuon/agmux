import { useEffect, useState } from "react";
import { api } from "../api/client";

/** Returns whether the named experimental feature is enabled (false while loading). */
export function useExperimental(name: string): boolean {
  const [enabled, setEnabled] = useState(false);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .listExperimental()
        .then((r) => {
          if (!cancelled) setEnabled(r.features.some((f) => f.name === name && f.enabled));
        })
        .catch(() => {});
    load();
    window.addEventListener("agmux:experimental-changed", load);
    return () => {
      cancelled = true;
      window.removeEventListener("agmux:experimental-changed", load);
    };
  }, [name]);
  return enabled;
}
