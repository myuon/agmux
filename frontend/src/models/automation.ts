import type { Automation } from "../api/client";

export function formatTrigger(
  a: Pick<Automation, "triggerType" | "triggerValue">
): string {
  if (a.triggerType === "queue") {
    return a.triggerValue
      ? `残タスクがある限り（前回終了の ${a.triggerValue} 後）`
      : "残タスクがある限り";
  }
  return a.triggerType === "interval"
    ? `${a.triggerValue} ごと`
    : `cron: ${a.triggerValue}`;
}
