import { fmtDateTime, healthState } from "../format";
import { useHealth } from "../health";
import type { Site } from "../types";

/** The overview's health panel (mockup 2: 站点体检): score, P0/P1/P2, the top issues. */
export function HealthPanel({ site, wide, onOpen }: { site: Site; wide?: boolean; onOpen: () => void }) {
  const { view, err, run } = useHealth(site.id);
  const latest = view?.latest ?? null;
  const running = view?.running || view?.last?.status === "running";
  const failed = view?.last?.status === "failed" ? view.last : null;
  return (
    <section className={"panel " + (wide ? "" : "p-health")}>
      <h2>站点体检 <button className="link" onClick={onOpen}>全部 ›</button></h2>
      {err && <p className="error">{err}</p>}
      {!latest ? (
        <p>{running ? <><i className="spin" /> 正在体检（最多 300 页，几分钟）…</> : "还没有体检记录。每天 08:00 自动体检。"}</p>
      ) : (
        <>
          <div className="health-head">
            <div className={"health-score st-" + healthState(latest)}><b>{latest.score}</b><small>健康分</small></div>
            <div className="health-counts">
              <span className="pc pc-0">P0 <b>{latest.p0}</b></span><span className="pc pc-1">P1 <b>{latest.p1}</b></span><span className="pc pc-2">P2 <b>{latest.p2}</b></span>
            </div>
            <small className="health-at">体检 {fmtDateTime(latest.finishedAt)}</small>
          </div>
          <ul className="health-list">
            {view!.issues.slice(0, 4).map((is) => (
              <li key={is.code}>
                <span className={"pri pri-" + is.severity}>{is.severity.toUpperCase()}</span>
                <span className="hl-main"><b>{is.title}（{is.count}）{is.new && <span className="chg new">新</span>}</b><small>{is.urls[0] ?? ""}</small></span>
              </li>
            ))}
            {view!.issues.length === 0 && <li><span className="st-good">● 没有发现问题</span></li>}
          </ul>
        </>
      )}
      {failed && <p className="error">上次体检失败（{fmtDateTime(failed.startedAt)}）：{failed.error}</p>}
      <div className="row">
        <button className="btn" disabled={running} onClick={run}>{running ? "体检中…" : "立即体检"}</button>
      </div>
    </section>
  );
}
