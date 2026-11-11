import { useEffect, useState } from "react";
import { api, errText, onEvent } from "../api";
import { coverageText, fmtTime, nextBackfillText, sliceStateText } from "../format";
import type { SiteProgress, SyncStatus } from "../types";

/** Settings → 同步: the pause switch and the sync progress table (进度表). */
export function SyncPanel() {
  const [paused, setPaused] = useState(false);
  const [rows, setRows] = useState<SiteProgress[]>([]);
  const [msg, setMsg] = useState("");

  const load = () => {
    api.syncPaused().then(setPaused).catch(() => {});
    api.syncProgress().then(setRows).catch(() => {});
  };
  useEffect(() => {
    load();
    // Progress has no push event of its own; refresh on the events that can
    // change it (a run finishing, pausing) and every so often besides, for
    // the days-covered count ticking forward during a long backfill.
    const offStatus = onEvent<SyncStatus>("sync:status", (s) => { if (s.state !== "running") load(); });
    const offPaused = onEvent<boolean>("sync:paused", load);
    const t = setInterval(load, 20000);
    return () => { offStatus(); offPaused(); clearInterval(t); };
  }, []);

  const toggle = async (on: boolean) => {
    setMsg("");
    try {
      await api.setSyncPaused(on);
      setPaused(on);
      load();
    } catch (e) {
      setMsg(errText(e));
    }
  };

  return (
    <section className="panel">
      <h2>同步</h2>
      <div className="row">
        <label className="check">
          <input type="checkbox" checked={paused} onChange={(e) => toggle(e.target.checked)} /> 暂停自动同步
        </label>
      </div>
      <p className="hint">暂停后，Search Console / GA4 / Bing 都不会再自动或手动同步，直到取消暂停；担心触发限流时可以先暂停。</p>
      {msg && <p className="error">{msg}</p>}
      {rows.length > 0 && (
        <table className="qtable sync-progress">
          <thead>
            <tr><th>站点</th><th>数据源</th><th>统计维度</th><th>覆盖范围</th><th>最新数据</th><th>最近同步</th><th>状态</th><th>下次回填</th></tr>
          </thead>
          <tbody>
            {rows.flatMap((site) =>
              (site.sources ?? []).flatMap((src) => {
                const slices = src.slices && src.slices.length > 0 ? src.slices : [null];
                return slices.map((sl, i) => (
                  <tr key={`${site.siteId}-${src.source}-${sl?.slice ?? i}`}>
                    {i === 0 ? <td className="mono" rowSpan={slices.length}>{site.host}</td> : null}
                    {i === 0 ? <td rowSpan={slices.length}>{src.label}</td> : null}
                    <td>{sl?.label ?? "—"}</td>
                    <td>{coverageText(sl)}</td>
                    <td className="mono">{sl?.newestDay || "—"}</td>
                    <td>{sl?.lastFetched ? fmtTime(sl.lastFetched) : "—"}</td>
                    <td>{sliceStateText(src, sl)}</td>
                    {i === 0 ? <td rowSpan={slices.length}>{nextBackfillText(src)}</td> : null}
                  </tr>
                ));
              }),
            )}
          </tbody>
        </table>
      )}
    </section>
  );
}
