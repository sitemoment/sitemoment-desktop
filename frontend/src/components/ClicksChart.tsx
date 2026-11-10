import { useEffect, useRef, useState } from "react";
import { fmtInt, niceScale } from "../format";
import type { DayPoint } from "../types";

const H = 200;
const M = { l: 44, r: 52, t: 10, b: 24 };

export function ClicksChart({ cur, prev }: { cur: DayPoint[]; prev: DayPoint[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(600);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(Math.max(280, el.clientWidth)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const n = cur.length;
  if (n === 0) return <div className="chart-plot" ref={ref} />;
  const values = [...cur, ...prev].map((d) => d.clicks);
  const { lo, hi, ticks } = niceScale(Math.min(...values), Math.max(...values));
  const x = (i: number) => M.l + (n === 1 ? 0 : i / (n - 1)) * (w - M.l - M.r);
  const y = (v: number) => M.t + (1 - (v - lo) / (hi - lo || 1)) * (H - M.t - M.b);
  const path = (a: DayPoint[]) => a.map((d, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(d.clicks).toFixed(1)}`).join("");
  const labels = Array.from(new Set([0, Math.round((n - 1) / 4), Math.round((n - 1) / 2), Math.round((3 * (n - 1)) / 4), n - 1]));
  const move = (e: React.MouseEvent<SVGRectElement>) => {
    const r = e.currentTarget.ownerSVGElement!.getBoundingClientRect();
    const i = Math.round(((e.clientX - r.left - M.l) / (w - M.l - M.r)) * (n - 1));
    setHover(Math.max(0, Math.min(n - 1, i)));
  };
  const h = hover;
  return (
    <>
      <div className="chart-plot" ref={ref}>
        <svg width={w} height={H} role="img" aria-label="每日点击：本期与上期">
          {ticks.map((v) => (
            <g key={v}>
              <line className="grid" x1={M.l} x2={w - M.r} y1={y(v)} y2={y(v)} />
              <text className="axis" x={M.l - 8} y={y(v) + 4} textAnchor="end">{fmtInt(v)}</text>
            </g>
          ))}
          {labels.map((i) => <text key={i} className="axis" x={x(i)} y={H - 6} textAnchor="middle">{cur[i].day.slice(5)}</text>)}
          {prev.length > 0 && <path className="ln ln-2" d={path(prev.slice(0, n))} />}
          <path className="ln ln-1" d={path(cur)} />
          <text className="dl" x={x(n - 1) + 8} y={y(cur[n - 1].clicks) + 4}>本期</text>
          {prev[n - 1] && <text className="dl" x={x(n - 1) + 8} y={y(prev[n - 1].clicks) + 4}>上期</text>}
          {h !== null && (
            <>
              <line className="xh" x1={x(h)} x2={x(h)} y1={M.t} y2={H - M.b} />
              {prev[h] && <circle className="dot" r={4} cx={x(h)} cy={y(prev[h].clicks)} fill="var(--series-2)" />}
              <circle className="dot" r={4} cx={x(h)} cy={y(cur[h].clicks)} fill="var(--series-1)" />
            </>
          )}
          <rect x={M.l} y={0} width={Math.max(0, w - M.l - M.r)} height={H} fill="transparent" onMouseMove={move} onMouseLeave={() => setHover(null)} />
        </svg>
        {h !== null && (
          <div className="tip" style={{ left: Math.max(0, Math.min(x(h) + 12, w - 190)) }}>
            <b>{cur[h].day}</b>
            <span><i className="lg" />本期 {fmtInt(cur[h].clicks)}</span>
            {prev[h] && <span><i className="lg lg-2" />上期 {fmtInt(prev[h].clicks)}（{prev[h].day}）</span>}
          </div>
        )}
      </div>
      <details className="chart-table">
        <summary>查看数据表</summary>
        <table>
          <tbody>
            {cur.map((d, i) => <tr key={d.day}><td>{d.day}</td><td>{fmtInt(d.clicks)}</td><td>{prev[i]?.day ?? ""}</td><td>{prev[i] ? fmtInt(prev[i].clicks) : ""}</td></tr>)}
          </tbody>
        </table>
      </details>
    </>
  );
}
