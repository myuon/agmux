import type { Automation } from "../api/client";

export function formatTrigger(
  a: Pick<Automation, "triggerType" | "triggerValue">
): string {
  return a.triggerType === "interval"
    ? `${a.triggerValue} ごと`
    : `cron: ${a.triggerValue}`;
}
