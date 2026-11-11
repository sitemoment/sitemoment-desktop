import { useState } from "react";
import { fmtCredits } from "../format";
import type { Card, ToolView } from "../types";

export const toolKindLabel: Record<ToolView["kind"], string> = {
  local: "本地数据", web: "免费工具", collector: "采集器", cloud: "SiteMoment 数据",
};

const stateText: Record<ToolView["status"], string> = { running: "进行中", ok: "完成", error: "失败", declined: "未执行" };

function meta(t: ToolView): string {
  const parts: string[] = [];
  if (t.ms > 0) parts.push(`${(t.ms / 1000).toFixed(1)} 秒`);
  if (t.cached) parts.push("缓存");
  if (t.credits > 0) parts.push(`${fmtCredits(t.credits)} 积分`);
  if (t.error) parts.push(t.error);
  return parts.join(" · ");
}

function cell(v: unknown): string {
  if (v == null) return "—";
  if (typeof v === "number") return Number.isInteger(v) ? v.toLocaleString("en-US") : v.toFixed(2);
  return String(v);
}

function CardBody({ card }: { card: Card }) {
  return (
    <div className="tool-card">
      {card.type === "table" ? (
        <table className="qtable">
          {card.columns && <thead><tr>{card.columns.map((c, i) => <th key={i}>{cell(c)}</th>)}</tr></thead>}
          <tbody>{(card.rows ?? []).map((r, i) => <tr key={i}>{r.map((c, j) => <td key={j} className={typeof c === "number" ? "num" : ""}>{cell(c)}</td>)}</tr>)}</tbody>
        </table>
      ) : (
        <ul className="tool-lines">{(card.items ?? []).map((l, i) => <li key={i} className={"st-" + (l.s === "ok" ? "good" : l.s === "warn" ? "warn" : "bad")}>{l.t}</li>)}</ul>
      )}
      {card.note && <p className="src-note">{card.note}</p>}
    </div>
  );
}

/** One tool call in the agent log; click to see its card. */
export function ToolCard({ tool }: { tool: ToolView }) {
  const [open, setOpen] = useState(false);
  const card = tool.card ?? null;
  return (
    <div className={`tool tool-${tool.kind} is-${tool.status}`}>
      <button className="tool-row" disabled={!card} onClick={() => setOpen((o) => !o)}>
        <span className="tool-icon" aria-hidden="true">
          {tool.status === "running" ? <i className="spin" /> : tool.status === "ok" ? "✓" : tool.status === "error" ? "✗" : "–"}
        </span>
        <span className="tool-body">
          <span className="tool-kind">{toolKindLabel[tool.kind] ?? tool.kind}</span>
          <span className="tool-title">{tool.label}</span>
          <span className="tool-meta">{meta(tool)}</span>
        </span>
        <span className="tool-state">{stateText[tool.status]}{card ? (open ? " ▴" : " ▾") : ""}</span>
      </button>
      {open && card && <CardBody card={card} />}
    </div>
  );
}
