import { CollapsibleText } from "../ui/CollapsibleText";
import type { StreamDisplayItem } from "../../models/stream";
import { ToolCallView } from "./ToolCallView";

function SystemEventView({ item }: { item: Extract<StreamDisplayItem, { kind: "system_event" }> }) {
  return (
    <div className="flex items-center gap-2 py-1 px-3 text-xs text-gray-500 border-y border-dashed border-gray-200">
      <span className="text-gray-400">{"\u27E1"}</span>
      <span>{item.label}</span>
      {item.detail && <span className="text-gray-400">{item.detail}</span>}
    </div>
  );
}

export function StreamDisplayItemView({ item, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded }: {
  item: StreamDisplayItem;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
}) {
  if (item.kind === "text") {
    return <CollapsibleText text={item.text} />;
  }
  if (item.kind === "image") {
    return (
      <img
        src={`data:${item.mediaType};base64,${item.data}`}
        alt="attached"
        className="max-w-xs max-h-48 rounded border border-gray-200"
      />
    );
  }
  if (item.kind === "tool_call") {
    return <ToolCallView item={item} onAnswer={onAnswer} sessionId={sessionId} escalationId={escalationId} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={onEscalationResponded} />;
  }
  if (item.kind === "system_event") {
    return <SystemEventView item={item} />;
  }
  return null;
}
