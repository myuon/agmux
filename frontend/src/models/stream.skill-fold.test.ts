import { describe, expect, it } from "vitest";
import { mergeStreamEntries, type StreamEntry } from "./stream";

const asEntries = (lines: unknown[]) => lines as unknown as StreamEntry[];
const items = (lines: unknown[]) => mergeStreamEntries(asEntries(lines)).flatMap((g) => g.items);

const skillUse = (id: string, msg = "msg_1") => ({
  type: "assistant",
  message: { id: msg, role: "assistant", content: [{ type: "tool_use", id, name: "Skill", input: { skill: "commit" } }] },
});
const bashUse = (id: string, msg = "msg_1") => ({
  type: "assistant",
  message: { id: msg, role: "assistant", content: [{ type: "tool_use", id, name: "Bash", input: { command: "ls" } }] },
});
const toolResult = (id: string, content: string, parent: string | null = null) => ({
  type: "user",
  parent_tool_use_id: parent,
  message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content }] },
});
const synthText = (text: string) => ({
  type: "user",
  parent_tool_use_id: null,
  isSynthetic: true,
  message: { role: "user", content: [{ type: "text", text }] },
});

describe("Skill content folding", () => {
  it("folds skill content when a parallel tool call and rate_limit_event sit in between", () => {
    const result = items([
      bashUse("toolu_bash"),
      skillUse("toolu_skill"),
      { type: "rate_limit_event", rate_limit_info: { status: "allowed" } },
      toolResult("toolu_bash", "ok"),
      toolResult("toolu_skill", "Launching skill: commit"),
      synthText("Base directory for this skill: /x\n\n# Commit"),
    ]);
    expect(result.filter((i) => i.kind === "text")).toHaveLength(0);
    const skill = result.find((i) => i.kind === "tool_call" && i.name === "Skill");
    expect(skill).toMatchObject({ result: expect.stringContaining("Base directory for this skill") });
  });

  it("folds when system/tool_progress and another thread's lines are interleaved", () => {
    const result = items([
      skillUse("toolu_skill"),
      { type: "system", subtype: "task_progress", task_id: "t" },
      { type: "tool_progress", tool_use_id: "x", parent_tool_use_id: "toolu_agent" },
      toolResult("toolu_other", "sub", "toolu_agent"),
      toolResult("toolu_skill", "Launching skill: commit"),
      synthText("Base directory for this skill: /x"),
    ]);
    expect(result.filter((i) => i.kind === "text")).toHaveLength(0);
  });

  it("folds both the re-invocation notice and the skill content", () => {
    const result = items([
      skillUse("toolu_skill"),
      toolResult("toolu_skill", "Launching skill: commit"),
      synthText("(Re-invocation of /commit — ...)"),
      synthText("Base directory for this skill: /x"),
    ]);
    expect(result.filter((i) => i.kind === "text")).toHaveLength(0);
    const skill = result.find((i) => i.kind === "tool_call" && i.name === "Skill");
    expect(skill).toMatchObject({ result: expect.stringContaining("Re-invocation") });
    expect(skill).toMatchObject({ result: expect.stringContaining("Base directory") });
  });

  it("does not fold user text after the next assistant turn", () => {
    const result = items([
      skillUse("toolu_skill"),
      toolResult("toolu_skill", "Launching skill: commit"),
      { type: "assistant", message: { id: "msg_2", role: "assistant", content: [{ type: "text", text: "done" }] } },
      { type: "user", message: { role: "user", content: [{ type: "text", text: "hello" }] } },
    ]);
    expect(result.filter((i) => i.kind === "text").map((i) => (i as { text: string }).text)).toEqual(["done", "hello"]);
  });
});
