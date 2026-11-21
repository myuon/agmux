import type { Session } from "../types/session";

// Only "needs your attention" lamps are colored: unread (blue, see lampClass)
// and waiting_input (orange). Everything else is muted gray; working pulses so
// activity is still visible without drawing the eye.
export const statusDots: Record<Session["status"], string> = {
  working: "bg-gray-400 animate-pulse",
  idle: "bg-blue-500",
  paused: "bg-gray-300",
  exited: "bg-gray-300",
  waiting_input: "bg-orange-500",
  archived: "bg-gray-200",
};

export const READ_RING = "bg-transparent border border-gray-300";

/**
 * Background class of a status lamp (#859). An idle session the user has
 * already seen gets a faint hollow ring instead of a filled lamp, so rows keep
 * their alignment without leaving an odd blank;
 * an idle session with an unseen finished turn keeps the blue lamp, which now
 * reads as "unread". Other statuses are unchanged.
 */
export function lampClass(status: string, unread?: boolean): string {
  if (status === "idle" && !unread) return READ_RING;
  return (statusDots as Record<string, string>)[status] ?? "bg-gray-300";
}

/** Accessible label of a lamp: only the unread lamp needs one. */
export function lampLabel(status: string, unread?: boolean): string | undefined {
  return status === "idle" && unread ? "未読" : undefined;
}

export function StatusDot({ status, unread }: { status: Session["status"]; unread?: boolean }) {
  const label = lampLabel(status, unread);
  return (
    <span
      className={`w-2 h-2 rounded-full shrink-0 ${lampClass(status, unread)}`}
      data-unread={label ? "true" : undefined}
      role={label ? "img" : undefined}
      aria-label={label}
      title={label}
    />
  );
}

export function StatusBadge({ status, unread }: { status: Session["status"]; unread?: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusDot status={status} unread={unread} />
      <span className="text-xs text-gray-500">{status}</span>
    </span>
  );
}
