import type { Session } from "../types/session";

const statusStyles: Record<Session["status"], string> = {
  working: "bg-green-100 text-green-700 border-green-200",
  idle: "bg-blue-100 text-blue-700 border-blue-200",
  paused: "bg-yellow-100 text-yellow-700 border-yellow-200",
  question_waiting: "bg-orange-100 text-orange-700 border-orange-200",
  alignment_needed: "bg-red-100 text-red-700 border-red-200",
  stopped: "bg-gray-100 text-gray-500 border-gray-200",
};

const statusLabels: Record<Session["status"], string> = {
  working: "Working",
  idle: "Idle",
  paused: "Paused",
  question_waiting: "Question",
  alignment_needed: "Alignment",
  stopped: "Stopped",
};

export function StatusBadge({ status }: { status: Session["status"] }) {
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full border ${statusStyles[status]}`}
    >
      {statusLabels[status]}
    </span>
  );
}
