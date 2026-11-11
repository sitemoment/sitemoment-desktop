import { describe, expect, it } from "vitest";
import type { AgentEvent, SessionView, ToolView, Transcript } from "../types";
import { agentReducer, emptyAgent, type AgentState } from "./reducer";

const session: SessionView = { id: 7, siteId: 1, title: "", spendMode: "confirm", status: "idle", credits: 0, updatedAt: 0 };
const loaded = agentReducer(emptyAgent, { type: "load", transcript: { session, items: [], pending: null } as Transcript });
const ev = (e: Record<string, unknown>) => ({ type: "event" as const, event: { sessionId: 7, turn: 1, ...e } as AgentEvent });
const run = (s: AgentState, ...events: Record<string, unknown>[]) => events.reduce((acc, e) => agentReducer(acc, ev(e)), s);
const tool = (status: ToolView["status"]): ToolView => ({
  callId: "c1", tool: "gsc_query", label: "GSC 查询", kind: "local", status, cached: false, credits: 0, ms: 0,
});

describe("agentReducer", () => {
  it("adds the user's message and marks the session running", () => {
    const s = agentReducer(loaded, { type: "sent", text: "hello", at: 1 });
    expect(s.items).toEqual([{ kind: "user", turn: 1, text: "hello", at: 1 }]);
    expect(s.session?.status).toBe("running");
  });

  it("turns streamed text into a note when tool calls follow", () => {
    const s = run(loaded, { type: "turn_started" }, { type: "text", delta: "先查" }, { type: "text", delta: "数据" });
    expect(s.streaming).toBe("先查数据");
    const n = run(s, { type: "note", content: "先查数据" });
    expect(n.streaming).toBe("");
    expect(n.items.map((i) => [i.kind, i.text])).toEqual([["note", "先查数据"]]);
  });

  it("updates a tool card in place when it finishes", () => {
    const s = run(loaded, { type: "tool_started", tool: tool("running") }, { type: "tool_finished", tool: { ...tool("ok"), ms: 120 } });
    expect(s.items).toHaveLength(1);
    expect(s.items[0].tool?.status).toBe("ok");
    expect(s.items[0].tool?.ms).toBe(120);
  });

  it("holds the turn for confirmation", () => {
    const pending = { calls: [{ callId: "c2", tool: "keyword_difficulty", label: "关键词难度", maxCredits: 2 }], totalMax: 2 };
    const s = run(loaded, { type: "turn_started" }, { type: "confirm_needed", pending }, { type: "turn_finished", status: "awaiting_confirm", credits: 0 });
    expect(s.pending).toEqual(pending);
    expect(s.session?.status).toBe("awaiting_confirm");
    const c = agentReducer(s, { type: "confirmed" });
    expect(c.pending).toBeNull();
    expect(c.session?.status).toBe("running");
  });

  it("finalizes the answer and goes idle", () => {
    const s = run(loaded, { type: "turn_started" }, { type: "text", delta: "**结论**" }, { type: "message", content: "**结论**：可以做" },
      { type: "turn_finished", status: "idle", credits: 1.2 });
    expect(s.streaming).toBe("");
    expect(s.items.map((i) => [i.kind, i.text])).toEqual([["answer", "**结论**：可以做"]]);
    expect(s.session?.status).toBe("idle");
  });

  it("keeps partial text on stop and on error", () => {
    const stopped = run(loaded, { type: "text", delta: "半句" }, { type: "stopped", content: "（已停止）" });
    expect(stopped.items.map((i) => i.kind)).toEqual(["note", "stopped"]);
    const failed = run(loaded, { type: "error", message: "超时" });
    expect(failed.items.map((i) => [i.kind, i.text])).toEqual([["error", "超时"]]);
  });

  it("ignores events of other sessions", () => {
    const s = agentReducer(loaded, { type: "event", event: { sessionId: 8, turn: 1, type: "text", delta: "x" } });
    expect(s).toBe(loaded);
  });
});
