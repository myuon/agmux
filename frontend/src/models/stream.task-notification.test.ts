import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";
import { backgroundTaskStatusLabel } from "../components/session/ToolCallView";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

const labelOf = (status: string, summary?: string) => {
  const groups = mergeStreamEntries(
    asEntries([{ type: "system", subtype: "task_notification", task_id: "a5158371c7438e1d4", status, summary }]),
  );
  const items = groups.flatMap((g) => g.items);
  return items.find((i) => i.kind === "system_event");
};

describe("standalone task_notification", () => {
  it("labels dismissed in Japanese", () => {
    expect(labelOf("dismissed")).toMatchObject({ eventType: "task_notification", label: "タスク取り消し" });
  });

  it("labels stopped in Japanese and keeps the summary", () => {
    expect(labelOf("stopped", 'Background agent "x" didn\'t finish')).toMatchObject({
      label: "タスク停止",
      detail: 'Background agent "x" didn\'t finish',
    });
  });

  it("labels completed / failed", () => {
    expect(labelOf("completed")).toMatchObject({ label: "タスク完了" });
    expect(labelOf("failed")).toMatchObject({ label: "タスク失敗" });
  });
});

describe("backgroundTaskStatusLabel", () => {
  it("labels dismissed in Japanese", () => {
    expect(backgroundTaskStatusLabel("dismissed").label).toBe("バックグラウンド取り消し");
  });
});
