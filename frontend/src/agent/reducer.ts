import type { AgentEvent, Item, Pending, SessionView, ToolView, Transcript } from "../types";

/** What the Agent panel shows for one session. */
export interface AgentState {
  session: SessionView | null;
  items: Item[];
  pending: Pending | null;
  /** Answer text streaming in for the current model call. */
  streaming: string;
}

export const emptyAgent: AgentState = { session: null, items: [], pending: null, streaming: "" };

export type AgentAction =
  | { type: "clear" }
  | { type: "load"; transcript: Transcript }
  | { type: "sent"; text: string; at: number }
  | { type: "confirmed" }
  | { type: "event"; event: AgentEvent };

const lastTurn = (items: Item[]) => items.reduce((n, it) => Math.max(n, it.turn), 0);

function withStatus(s: AgentState, status: SessionView["status"]): AgentState {
  return s.session ? { ...s, session: { ...s.session, status } } : s;
}

/** Puts a tool card in place (same callId) or appends it. */
function upsertTool(items: Item[], turn: number, tool: ToolView): Item[] {
  const i = items.findIndex((it) => it.kind === "tool" && it.tool?.callId === tool.callId && it.turn === turn);
  const item: Item = { kind: "tool", turn, tool, at: Date.now() / 1000 };
  if (i < 0) return [...items, item];
  const next = items.slice();
  next[i] = { ...items[i], tool };
  return next;
}

/** Keeps text that was streaming when a turn ended early as a note. */
function flush(s: AgentState, turn: number): Item[] {
  return s.streaming.trim() ? [...s.items, { kind: "note", turn, text: s.streaming, at: Date.now() / 1000 }] : s.items;
}

export function agentReducer(s: AgentState, a: AgentAction): AgentState {
  switch (a.type) {
    case "clear":
      return emptyAgent;
    case "load":
      return { session: a.transcript.session, items: a.transcript.items ?? [], pending: a.transcript.pending ?? null, streaming: "" };
    case "sent": {
      // A new message while paid calls wait for approval declines them (the Go side does the same).
      const items = [...s.items, { kind: "user" as const, turn: lastTurn(s.items) + 1, text: a.text, at: a.at }];
      return withStatus({ ...s, items, pending: null, streaming: "" }, "running");
    }
    case "confirmed":
      return withStatus({ ...s, pending: null }, "running");
    case "event":
      break;
  }
  const ev = a.event;
  if (!s.session || ev.sessionId !== s.session.id) return s;
  switch (ev.type) {
    case "turn_started":
      return withStatus({ ...s, streaming: "" }, "running");
    case "text":
      return { ...s, streaming: s.streaming + ev.delta };
    case "note":
      return { ...s, items: [...s.items, { kind: "note", turn: ev.turn, text: ev.content, at: Date.now() / 1000 }], streaming: "" };
    case "tool_started":
    case "tool_finished":
      return { ...s, items: upsertTool(s.items, ev.turn, ev.tool) };
    case "confirm_needed":
      return withStatus({ ...s, items: flush(s, ev.turn), streaming: "", pending: ev.pending }, "awaiting_confirm");
    case "message":
      return { ...s, items: [...s.items, { kind: "answer", turn: ev.turn, text: ev.content, at: Date.now() / 1000 }], streaming: "" };
    case "error":
      return { ...s, items: [...flush(s, ev.turn), { kind: "error", turn: ev.turn, text: ev.message, at: Date.now() / 1000 }], streaming: "" };
    case "stopped":
      return { ...s, items: [...flush(s, ev.turn), { kind: "stopped", turn: ev.turn, text: ev.content, at: Date.now() / 1000 }], streaming: "" };
    case "turn_finished": {
      const next = withStatus({ ...s, streaming: "" }, ev.status);
      return ev.status === "awaiting_confirm" ? next : { ...next, pending: null };
    }
  }
  return s;
}
