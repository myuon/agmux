import { describe, expect, it } from "vitest";
import { resultErrorFooter } from "./resultErrorFooter";

describe("resultErrorFooter", () => {
  it("hides subtype success on an error result", () => {
    expect(resultErrorFooter("success", "api_error")).toBe("api_error");
  });
  it("keeps error subtypes", () => {
    expect(resultErrorFooter("error_during_execution", "aborted_streaming")).toBe("error_during_execution · aborted_streaming");
  });
  it("returns empty when nothing to show", () => {
    expect(resultErrorFooter("success", undefined)).toBe("");
  });
});
