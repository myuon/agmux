import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system session_writes_lost event", () => {
  it("shows a warning with the lost items and error code", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "session_writes_lost",
          error_code: "ENOSPC",
          lost: ["transcript"],
          transcript_session_ids: ["c6e42c10-aa46-49bb-b42e-ce83c279d972"],
          session_id: "c6e42c10-aa46-49bb-b42e-ce83c279d972",
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "session_writes_lost",
      label: "⚠ transcript を保存できませんでした (ENOSPC)",
      detail: "ディスクの空き容量が不足しています",
    });
  });

  it("falls back when lost and error_code are missing", () => {
    const groups = mergeStreamEntries(asEntries([{ type: "system", subtype: "session_writes_lost" }]));
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      label: "⚠ セッションのデータ を保存できませんでした",
    });
  });
});
