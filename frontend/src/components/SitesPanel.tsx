import { healthIcon, healthState } from "../format";
import type { Account, Site, SiteHealth, SyncStatus } from "../types";

const healthTitle = { good: "健康", warn: "注意", bad: "有 P0" };

function State({ status, bound, health }: { status?: SyncStatus; bound: boolean; health?: SiteHealth }) {
  if (status?.state === "running") return <span className="site-state"><i className="spin" /> 同步</span>;
  if (status?.state === "failed") return <span className="site-state st-bad" title={status.error}>■ 失败</span>;
  if (health) {
    const st = healthState(health);
    return <span className={"site-state site-score st-" + st} title={`健康分 · ${healthTitle[st]}`}><i>{healthIcon[st]}</i>{health.score}</span>;
  }
  if (!bound) return <span className="site-state st-warn" title="还没有绑定 Search Console">▲ 未绑定</span>;
  return <span className="site-state st-good">●</span>;
}

export function SitesPanel(props: {
  sites: Site[]; selected: number | null; statuses: Record<number, SyncStatus>; account: Account | null;
  scores?: Record<number, SiteHealth>;
  loggingIn?: boolean;
  onSelect: (id: number) => void; onAdd: () => void; onLogin: () => void;
}) {
  const { sites, selected, statuses, account } = props;
  return (
    <aside className="side">
      <h6>站点 · {sites.length}</h6>
      <div className="sites">
        {sites.map((s) => (
          <button key={s.id} className={"site" + (s.id === selected ? " on" : "")} onClick={() => props.onSelect(s.id)}>
            <span className="site-fav">{s.host[0]?.toUpperCase()}</span>
            <span className="site-main">
              <span className="site-name">{s.host}</span>
              <span className="site-note">{s.gscProperty || "未连接数据源"}</span>
            </span>
            <State status={statuses[s.id]} bound={!!s.gscProperty} health={props.scores?.[s.id]} />
          </button>
        ))}
        <button className="site" onClick={props.onAdd}>
          <span className="site-fav">+</span>
          <span className="site-main"><span className="site-name">添加站点</span></span>
        </button>
      </div>
      <div className="acct">
        {account?.signedIn ? (
          <>
            <span className="acct-avatar">{(account.name || account.email)[0]?.toUpperCase()}</span>
            <span className="acct-text"><b>{account.name || account.email}</b><small>{account.plan || "free"}</small></span>
          </>
        ) : (
          <button className="btn" onClick={props.onLogin}>
            {props.loggingIn ? "等待浏览器…（取消）" : "登录 SiteMoment"}
          </button>
        )}
      </div>
    </aside>
  );
}
