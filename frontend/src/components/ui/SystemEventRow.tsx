import { useState } from "react";

// Task completion notifications can carry a summary of arbitrary length, which
// would otherwise fill the whole viewport on narrow screens.
const DETAIL_COLLAPSE_LENGTH = 120;

export function SystemEventRow({ label, detail }: { label: string; detail?: string }) {
  const [expanded, setExpanded] = useState(false);
  const collapsible = !!detail && (detail.length > DETAIL_COLLAPSE_LENGTH || detail.includes("\n"));

  return (
    <div
      className={`flex ${collapsible && expanded ? "items-start" : "items-center"} gap-2 py-1 px-3 text-xs text-gray-500 border-y border-dashed border-gray-200`}
    >
      <span className="shrink-0 text-gray-400">{"⟡"}</span>
      <span className="shrink-0">{label}</span>
      {detail && (
        <span
          className={`text-gray-400 ${collapsible ? (expanded ? "min-w-0 flex-1 whitespace-pre-wrap break-words" : "min-w-0 flex-1 truncate") : ""}`}
        >
          {detail}
        </span>
      )}
      {collapsible && (
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="shrink-0 text-blue-500 hover:text-blue-700"
        >
          {expanded ? "折りたたむ" : "展開"}
        </button>
      )}
    </div>
  );
}
