import { useEffect, useState } from "react";
import { api, errText, onEvent } from "../api";
import { BindGSC } from "../components/BindGSC";
import { BindSources } from "../components/BindSources";
import { ClicksChart } from "../components/ClicksChart";
import { HealthPanel } from "../components/HealthPanel";
import { KpiRow } from "../components/KpiRow";
import { QueriesTable } from "../components/QueriesTable";
import { fmtInt, fmtTime } from "../format";
import type { Overview, Site, SyncProgress, SyncStatus } from "../types";

// While a backfill is running, refetching only on status changes leaves the
// page blank for minutes (status stays "running" the whole time). Refetch on
// progress events too, but throttled: a backfill can emit one per request.
const PROGRESS_REFRESH_INTERVAL_MS = 5000;

export function OverviewPage({ site, status, paused, onChanged, onOpenHealth }: {
  site: Site; status?: SyncStatus; paused?: boolean; onChanged: () => void; onOpenHealth: () => void;
}) {
  const [days, setDays] = useState(28);
  const [ov, setOv] = useState<Overview | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!site.gscProperty) return;
    let live = true;
    api.overview(site.id, days).then((o) => live && setOv(o)).catch((e) => live && setErr(errText(e)));
    return () => { live = false; };
  }, [site.id, site.gscProperty, site.gaProperty, site.bingSite, days, status?.at]);

  const running = status?.state === "running";
  useEffect(() => {
    if (!site.gscProperty || !running) return;
    let live = true;
    let lastFetch = 0;
    const off = onEvent<SyncProgress>("sync:progress", (p) => {
      if (p.siteId !== site.id) return;
      const now = Date.now();
      if (now - lastFetch < PROGRESS_REFRESH_INTERVAL_MS) return;
      lastFetch = now;
      api.overview(site.id, days).then((o) => live && setOv(o)).catch((e) => live && setErr(errText(e)));
    });
    return () => { live = false; off(); };
  }, [site.id, site.gscProperty, days, running]);

  if (!site.gscProperty) {
    return (
      <BindGSC site={site} onBound={onChanged}>
        <BindSources site={site} onChanged={onChanged} />
      </BindGSC>
    );
  }
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
        <button className="btn" disabled={running || paused} title={paused ? "已暂停自动同步，先取消暂停" : undefined}
          onClick={() => api.syncNow(site.id).catch((e) => setErr(errText(e)))}>
          {paused ? "已暂停自动同步，先取消暂停" : running ? "同步中…" : "立即同步"}
        </button>
      </div>
      <div className="content">
        {err && <p className="error">{err}</p>}
        {!ov?.synced ? (
          <>
            <div className="empty">
              {paused ? "已暂停自动同步，先在设置 → 同步里取消暂停。" : running ? "正在同步 Search Console 数据。第一次会回填 16 个月，大约需要几分钟。" : "还没有数据，点「立即同步」。"}
            </div>
            <HealthPanel site={site} wide onOpen={onOpenHealth} />
          </>
        ) : (
          <>
            <KpiRow ov={ov} />
            <section className="panel p-chart">
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
            <HealthPanel site={site} onOpen={onOpenHealth} />
            <section className="panel">
              <h2>查询词 <span className="mono">前 {ov.queries.length} 个，按点击</span></h2>
              <QueriesTable rows={ov.queries} to={ov.to} />
            </section>
          </>
        )}
        <BindSources site={site} onChanged={onChanged} />
      </div>
    </>
  );
}
