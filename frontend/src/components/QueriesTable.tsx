import { fmtInt, fmtPct, posChange } from "../format";
import type { QueryStat } from "../types";

export function QueriesTable({ rows, to }: { rows: QueryStat[]; to: string }) {
  if (rows.length === 0) return <p className="src-note">这段时间没有查询词数据。</p>;
  return (
    <>
      <table className="qtable">
        <thead>
          <tr><th>查询词</th><th className="num">排名</th><th className="num">点击</th><th className="num">展示</th><th className="num">CTR</th></tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const d = posChange(r.position, r.prevPosition);
            return (
              <tr key={r.query}>
                <td>{r.query}</td>
                <td className="num">{r.position.toFixed(1)}{d ? <span className={"chg " + (d.up ? "up" : "down")}>{d.text}</span> : <span className="chg new">新</span>}</td>
                <td className="num">{fmtInt(r.clicks)}</td>
                <td className="num">{fmtInt(r.impressions)}</td>
                <td className="num">{fmtPct(r.impressions ? r.clicks / r.impressions : 0)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="src-note">来自 Google Search Console（网页搜索，截至 {to}），保存在本机。排名变化和上一个同样长度的时段比较。</p>
    </>
  );
}
