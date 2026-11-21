import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";

function isPageVisible(): boolean {
  return typeof document === "undefined" || document.visibilityState !== "hidden";
}

/**
 * Marks the session as read while its screen is shown (#859): when the
 * screen opens, when the tab / app comes back to the foreground, and whenever
 * a turn finishes while it is open (`unread` turns true). A hidden tab does
 * not count as seeing the session.
 */
export function useMarkSessionRead(sessionId: string, unread: boolean | undefined, onRead: () => void) {
  const [visible, setVisible] = useState(isPageVisible);
  useEffect(() => {
    const update = () => setVisible(isPageVisible());
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);

  const markRead = useCallback(() => {
    api.markSessionRead(sessionId).then(onRead).catch(() => {});
    // onRead only clears local state; a changing identity must not re-mark.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // Opening the screen (or returning to it) reads the session.
  useEffect(() => {
    if (visible) markRead();
  }, [visible, markRead]);

  // A turn that finishes while the screen is shown is read right away.
  useEffect(() => {
    if (visible && unread) markRead();
  }, [visible, unread, markRead]);
}
