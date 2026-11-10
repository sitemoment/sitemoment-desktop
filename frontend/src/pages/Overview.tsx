import { useEffect, useState } from "react";
import { api, errText } from "../api";
import { BindGSC } from "../components/BindGSC";
import { ClicksChart } from "../components/ClicksChart";
import { KpiRow } from "../components/KpiRow";
import { QueriesTable } from "../components/QueriesTable";
import { fmtInt, fmtTime } from "../format";
import type { Overview, Site, SyncStatus } from "../types";

export function OverviewPage({ site, status, onChanged }: { site: Site; status?: SyncStatus; onChanged: () => void }) {
  const [days, setDays] = useState(28);
  const [ov, setOv] = useState<Overview | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!site.gscProperty) return;
    let live = true;
    api.overview(site.id, days).then((o) => live && setOv(o)).catch((e) => live && setErr(errText(e)));
    return () => { live = false; };
  }, [site.id, site.gscProperty, days, status?.at]);

  if (!site.gscProperty) return <BindGSC site={site} onBound={onChanged} />;
  const running = status?.state === "running";
  return (
    <>
      <div className="toolbar">
        <span>Search Console <span className="mono">{site.gscProperty}</span></span>
        {status?.state === "done" && <span className="mono">已同步 {fmtTime(status.at)}</span>}
        {status?.state === "failed" && <span className="error">{status.error}</span>}
        <span className="grow" />
        <div className="seg">
          {[7, 28, 90].map((d) => <button key={d} className={d === days ? "on" : ""} onClick={() => setDays(d)}>{d}d</button>)}
        </div>
        <button className="btn" disabled={running} onClick={() => api.syncNow(site.id)}>{running ? "同步中…" : "立即同步"}</button>
      </div>
      <div className="content">
        {err && <p className="error">{err}</p>}
        {!ov?.synced ? (
          <div className="empty">{running ? "正在同步 Search Console 数据。第一次会回填 16 个月，大约需要几分钟。" : "还没有数据，点「立即同步」。"}</div>
        ) : (
          <>
            <KpiRow ov={ov} />
            <section className="panel">
              <div className="chart-head">
                <div>
                  <div className="chart-title">每日点击 · 本期与上期</div>
                  <div className="chart-sub">GSC · 网页搜索 · {ov.from} 至 {ov.to}，对比 {ov.prevFrom} 至 {ov.prevTo}</div>
                </div>
                <div className="legend">
                  <span><i className="lg" />本期 {fmtInt(ov.current.clicks)}</span>
                  <span><i className="lg lg-2" />上期 {fmtInt(ov.previous.clicks)}</span>
                </div>
              </div>
              <ClicksChart cur={ov.daily} prev={ov.prevDaily} />
            </section>
            <section className="panel">
              <h2>查询词 <span className="mono">前 {ov.queries.length} 个，按点击</span></h2>
              <QueriesTable rows={ov.queries} to={ov.to} />
            </section>
          </>
        )}
      </div>
    </>
  );
}
