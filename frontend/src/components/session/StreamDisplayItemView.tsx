import { CollapsibleText } from "../ui/CollapsibleText";
import { SystemEventRow } from "../ui/SystemEventRow";
import type { StreamDisplayItem } from "../../models/stream";
import { ToolCallView } from "./ToolCallView";

function SystemEventView({ item }: { item: Extract<StreamDisplayItem, { kind: "system_event" }> }) {
  return <SystemEventRow label={item.label} detail={item.detail} />;
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
  if (item.kind === "thinking") {
    return (
      <details className="text-xs text-gray-400">
        <summary className="cursor-pointer select-none hover:text-gray-600">Thinking...</summary>
        <pre className="mt-1 whitespace-pre-wrap text-gray-500 bg-gray-50 rounded p-2 overflow-x-auto">{item.text}</pre>
      </details>
    );
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
