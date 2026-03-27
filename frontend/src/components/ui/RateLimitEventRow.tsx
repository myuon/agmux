import type { StreamDisplayItem } from "../../models/stream";

type RateLimitItem = Extract<StreamDisplayItem, { kind: "rate_limit" }>;

function formatResetTime(epochSeconds: number): string {
  if (!epochSeconds) return "";
  const date = new Date(epochSeconds * 1000);
  const now = new Date();
  const diffMs = date.getTime() - now.getTime();
  if (diffMs <= 0) return "リセット済み";
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 60) return `${diffMin}分後にリセット`;
  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) return `${diffHours}時間${diffMin % 60}分後にリセット`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}日${diffHours % 24}時間後にリセット`;
}

function rateLimitTypeLabel(type: string): string {
  if (type === "five_hour") return "5時間";
  if (type === "seven_day") return "7日間";
  return type;
}

export function RateLimitEventRow({ item }: { item: RateLimitItem }) {
  const isWarning = item.status === "allowed_warning";
  const isRejected = item.status === "rejected";
  const resetLabel = formatResetTime(item.resetsAt);
  const windowLabel = rateLimitTypeLabel(item.rateLimitType);

  // Colors based on status
  const borderColor = isRejected
    ? "border-red-300"
    : isWarning
      ? "border-amber-300"
      : "border-gray-200";
  const bgColor = isRejected
    ? "bg-red-50"
    : isWarning
      ? "bg-amber-50"
      : "bg-gray-50";
  const textColor = isRejected
    ? "text-red-700"
    : isWarning
      ? "text-amber-700"
      : "text-gray-500";
  const iconColor = isRejected
    ? "text-red-400"
    : isWarning
      ? "text-amber-400"
      : "text-gray-400";

  const statusLabel = isRejected
    ? "レート制限超過"
    : isWarning
      ? "レート制限警告"
      : "レート制限";

  const utilizationPercent = item.utilization != null
    ? Math.round(item.utilization * 100)
    : null;

  return (
    <div className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 py-1.5 px-3 text-xs ${textColor} border-y border-dashed ${borderColor} ${bgColor}`}>
      <span className="inline-flex items-center gap-1 whitespace-nowrap">
        <span className={iconColor}>{isRejected ? "\u26D4" : "\u26A0"}</span>
        <span className="font-medium">{statusLabel}</span>
        <span className="text-gray-400">({windowLabel})</span>
      </span>
      {utilizationPercent != null && (
        <span className="inline-flex items-center gap-1 whitespace-nowrap">
          <span className="inline-block w-16 h-1.5 bg-gray-200 rounded-full overflow-hidden">
            <span
              className={`block h-full rounded-full ${
                utilizationPercent >= 90
                  ? "bg-red-400"
                  : utilizationPercent >= 75
                    ? "bg-amber-400"
                    : "bg-green-400"
              }`}
              style={{ width: `${Math.min(utilizationPercent, 100)}%` }}
            />
          </span>
          <span>{utilizationPercent}%</span>
        </span>
      )}
      {item.overageStatus && (
        <span className="whitespace-nowrap text-gray-400">overage: {item.overageStatus}</span>
      )}
      {resetLabel && <span className="whitespace-nowrap text-gray-400 ml-auto">{resetLabel}</span>}
    </div>
  );
}
