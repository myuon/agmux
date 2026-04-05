import { CollapsibleText } from "../ui/CollapsibleText";
import { SystemEventRow } from "../ui/SystemEventRow";
import { RateLimitEventRow } from "../ui/RateLimitEventRow";
import { ApiRetryEventRow } from "../ui/ApiRetryEventRow";
import type { StreamDisplayItem } from "../../models/stream";
import { ToolCallView } from "./ToolCallView";
import { AlertTriangle, CheckCircle } from "lucide-react";

function SystemEventView({ item }: { item: Extract<StreamDisplayItem, { kind: "system_event" }> }) {
  return <SystemEventRow label={item.label} detail={item.detail} />;
}

export function StreamDisplayItemView({ item, onAnswer, sessionId, pendingPermission, onPermissionResponded }: {
  item: StreamDisplayItem;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  pendingPermission?: { id: string; toolName: string; input: unknown; timedOut?: boolean; timeoutSeconds?: number };
  onPermissionResponded?: () => void;
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
    return <ToolCallView item={item} onAnswer={onAnswer} sessionId={sessionId} pendingPermission={pendingPermission} onPermissionResponded={onPermissionResponded} />;
  }
  if (item.kind === "system_event") {
    return <SystemEventView item={item} />;
  }
  if (item.kind === "rate_limit") {
    return <RateLimitEventRow item={item} />;
  }
  if (item.kind === "api_retry") {
    return <ApiRetryEventRow item={item} />;
  }
  if (item.kind === "result") {
    if (item.isError) {
      return (
        <div className="flex items-start gap-2 rounded-lg border border-red-300 bg-red-50 px-3 py-2.5 text-xs text-red-800">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-500" />
          <div className="min-w-0 flex-1">
            <div className="font-semibold">エラーが発生しました</div>
            {item.result && (
              <pre className="mt-1 whitespace-pre-wrap break-words font-sans">{item.result}</pre>
            )}
          </div>
        </div>
      );
    }
    return (
      <div className="flex items-center gap-1.5 text-xs text-gray-400">
        <CheckCircle className="h-3 w-3 text-green-400" />
        <span>完了</span>
        {item.numTurns != null && <span>({item.numTurns}ターン)</span>}
        {item.durationMs != null && (
          <span>
            {item.durationMs >= 60000
              ? `${(item.durationMs / 60000).toFixed(1)}分`
              : `${(item.durationMs / 1000).toFixed(1)}秒`}
          </span>
        )}
        {item.costUsd != null && <span>${item.costUsd.toFixed(4)}</span>}
      </div>
    );
  }
  return null;
}
