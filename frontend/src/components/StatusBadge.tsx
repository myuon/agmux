import type { Session } from "../types/session";

export const statusDots: Record<Session["status"], string> = {
  working: "bg-green-500",
  idle: "bg-blue-500",
  paused: "bg-yellow-500",
  exited: "bg-red-500",
  waiting_input: "bg-orange-500",
};

export function StatusDot({ status }: { status: Session["status"] }) {
  return <span className={`w-2 h-2 rounded-full shrink-0 ${statusDots[status]}`} />;
}

export function StatusBadge({ status }: { status: Session["status"] }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusDot status={status} />
      <span className="text-xs text-gray-500">{status}</span>
    </span>
  );
}
