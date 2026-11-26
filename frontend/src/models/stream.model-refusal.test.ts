import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];

describe("system model_refusal_no_fallback event", () => {
  it("shows a warning with the refusal explanation", () => {
    const groups = mergeStreamEntries(
      asEntries([
        {
          type: "system",
          subtype: "model_refusal_no_fallback",
          original_model: "claude-opus-5-5",
          request_id: "req_xxx",
          api_refusal_category: "reasoning_extraction",
          api_refusal_explanation: "This request was blocked as it seems to violate the Terms of Service.",
          refused_user_message_uuid: "9935e8ed-c46e-4779-a640-6f049254a43f",
          content: "",
        },
      ]),
    );
    expect(groups).toHaveLength(1);
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      eventType: "model_refusal_no_fallback",
      label: "⚠ メッセージがモデルに拒否されました (claude-opus-5-5)",
      detail: "This request was blocked as it seems to violate the Terms of Service.",
    });
  });

  it("falls back when fields are missing", () => {
    const groups = mergeStreamEntries(asEntries([{ type: "system", subtype: "model_refusal_no_fallback" }]));
    expect(groups[0].items[0]).toMatchObject({
      kind: "system_event",
      label: "⚠ メッセージがモデルに拒否されました",
    });
  });
});
