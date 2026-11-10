import { change, fmtCompact, fmtPct, posChange, type Delta } from "../format";
import type { DayPoint, Overview } from "../types";

function Spark({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const min = Math.min(...values), max = Math.max(...values);
  const pts = values.map((v, i) => `${((i / (values.length - 1)) * 60 + 2).toFixed(1)},${(25 - ((v - min) / (max - min || 1)) * 22).toFixed(1)}`);
  return <svg width="64" height="28" aria-hidden="true"><polyline points={pts.join(" ")} fill="none" stroke="#2c7f8f" strokeWidth="1.5" /></svg>;
}

function Kpi({ label, value, delta, spark }: { label: string; value: string; delta: Delta; spark?: number[] }) {
  return (
    <div className="kpi">
      <div className="kpi-label">{label}<span className="mono">GSC</span></div>
      <div className="kpi-value">{value}</div>
      <div className="kpi-foot">
        {delta ? <span className={"delta " + (delta.up ? "up" : "down")}>{delta.up ? "↑" : "↓"} {delta.text}</span> : <span className="delta">—</span>}
        {spark && <Spark values={spark} />}
      </div>
    </div>
  );
}

const ctr = (d: DayPoint) => (d.impressions ? d.clicks / d.impressions : 0);

export function KpiRow({ ov }: { ov: Overview }) {
  const { current: c, previous: p } = ov;
  const pos = posChange(c.position, p.impressions ? p.position : null);
  return (
    <div className="kpis">
      <Kpi label="点击" value={fmtCompact(c.clicks)} delta={change(c.clicks, p.clicks)} spark={ov.daily.map((d) => d.clicks)} />
      <Kpi label="展示" value={fmtCompact(c.impressions)} delta={change(c.impressions, p.impressions)} spark={ov.daily.map((d) => d.impressions)} />
      <Kpi label="CTR" value={fmtPct(c.ctr)} delta={p.ctr ? { text: `${Math.abs((c.ctr - p.ctr) * 100).toFixed(2)} pt`, up: c.ctr >= p.ctr } : null} spark={ov.daily.map(ctr)} />
      <Kpi label="平均排名" value={c.position ? c.position.toFixed(1) : "—"} delta={pos && { text: pos.text.slice(1), up: pos.up }} />
    </div>
  );
}
