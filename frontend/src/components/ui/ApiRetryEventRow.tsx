import type { StreamDisplayItem } from "../../models/stream";

type Props = {
  item: Extract<StreamDisplayItem, { kind: "api_retry" }>;
};

function formatDelay(ms: number): string {
  if (ms >= 60000) {
    return `${(ms / 60000).toFixed(1)}分`;
  }
  return `${(ms / 1000).toFixed(1)}秒`;
}

export function ApiRetryEventRow({ item }: Props) {
  return (
    <div className="flex items-center gap-2 py-1 px-3 text-xs text-amber-600 bg-amber-50 border-y border-dashed border-amber-200">
      <span>🔄</span>
      <span>
        APIリトライ中 ({item.attempt}/{item.maxRetries})
      </span>
      {item.error && (
        <span className="text-amber-500">
          {item.error}{item.errorStatus ? ` (${item.errorStatus})` : ""}
        </span>
      )}
      <span className="ml-auto text-amber-400">
        待機 {formatDelay(item.retryDelayMs)}
      </span>
    </div>
  );
}
