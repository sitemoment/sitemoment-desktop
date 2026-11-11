import { useEffect, useState } from "react";
import { api, errText } from "../api";
import { fmtDate, fmtDateTime, fmtInt, healthIcon, healthState } from "../format";
import { useHealth } from "../health";
import type { Issue, PageFilter, PageList, Site } from "../types";

const filters: [PageFilter, string][] = [["all", "全部"], ["error", "报错"], ["noindex", "noindex"], ["redirect", "跳转"], ["not_indexed", "未收录"]];

function IssueRow({ is }: { is: Issue }) {
  const [open, setOpen] = useState(false);
  return (
    <li>
      <span className={"pri pri-" + is.severity}>{is.severity.toUpperCase()}</span>
      <span className="hl-main">
        <button className="link" onClick={() => setOpen(!open)}>
          <b>{is.title}（{is.count}）</b>{is.new && <span className="chg new">新</span>} {is.urls.length > 0 && (open ? "▾" : "▸")}
        </button>
        <small>{is.detail}</small>
        {open && <ul className="url-list">{is.urls.map((u) => <li key={u} className="mono">{u}</li>)}</ul>}
      </span>
    </li>
  );
}

/** 体检: the latest daily check, its history and the pages it remembers. */
export function HealthPage({ site }: { site: Site }) {
  const { view, err, run } = useHealth(site.id);
  const [filter, setFilter] = useState<PageFilter>("all");
  const [contains, setContains] = useState("");
  const [pages, setPages] = useState<PageList | null>(null);
  const [perr, setPerr] = useState("");
  const latestId = view?.latest?.id;
  useEffect(() => {
    let live = true;
    const t = setTimeout(() => {
      api.pages(site.id, filter, contains).then((p) => live && setPages(p), (e) => live && setPerr(errText(e)));
    }, 200);
    return () => { live = false; clearTimeout(t); };
  }, [site.id, filter, contains, latestId]);

  const latest = view?.latest ?? null;
  const running = view?.running || view?.last?.status === "running";
  const s = view?.summary;
  return (
    <>
      <div className="toolbar">
        {latest ? (
          <>
            <span className={"health-inline st-" + healthState(latest)}>{healthIcon[healthState(latest)]} 健康分 <b className="mono">{latest.score}</b></span>
            <span className="mono">P0 {latest.p0} · P1 {latest.p1} · P2 {latest.p2}</span>
            <span className="mono">体检 {fmtDateTime(latest.finishedAt)}</span>
          </>
        ) : <span>还没有体检记录</span>}
        <span className="grow" />
        <span className="mono">每天 08:00 自动体检</span>
        <button className="btn" disabled={running} onClick={run}>{running ? "体检中…" : "立即体检"}</button>
      </div>
      <div className="content">
        {err && <p className="error kw-msg">{err}</p>}
        {view?.last?.status === "failed" && <p className="error kw-msg">上次体检失败（{fmtDateTime(view.last.startedAt)}）：{view.last.error}</p>}
        {latest && s && (
          <section className="panel">
            <h2>问题 <span className="mono">{view!.issues.length} 类</span></h2>
            <p className="src-note">
              抓取 {s.pages} 页（{s.ok} 页正常{s.truncated ? "，达到 300 页上限" : ""}），sitemap {fmtInt(s.sitemapUrls)} 个网址
              {s.disallowed > 0 && `，robots.txt 不让抓的 ${s.disallowed} 个`}
              {s.rechecked > 0 && `，复查以前正常的页面 ${s.rechecked} 个`}
              {s.gsc && `；GSC ${s.gsc.from} 至 ${s.gsc.to} 点击 ${fmtInt(s.gsc.clicks)}（前 28 天 ${fmtInt(s.gsc.prevClicks)}）`}
              {s.inspected > 0 && `；网址检查 ${s.inspected} 个`}
              {s.inspectNote && `（${s.inspectNote}）`}
            </p>
            <ul className="health-list">
              {view!.issues.map((is) => <IssueRow key={is.code} is={is} />)}
              {view!.issues.length === 0 && <li><span className="st-good">● 没有发现问题</span></li>}
            </ul>
          </section>
        )}
        <section className="panel">
          <h2>页面 <span className="mono">{pages ? `${pages.total} 个` : ""}</span></h2>
          <div className="row">
            <div className="seg">{filters.map(([f, label]) => <button key={f} className={f === filter ? "on" : ""} onClick={() => setFilter(f)}>{label}</button>)}</div>
            <input className="input" placeholder="网址包含…" value={contains} onChange={(e) => setContains(e.target.value)} />
          </div>
          {perr && <p className="error">{perr}</p>}
          <table className="qtable">
            <thead><tr><th>网址</th><th className="num">状态</th><th>收录</th><th>标题</th><th className="num">上次正常</th></tr></thead>
            <tbody>
              {pages?.pages.map((p) => (
                <tr key={p.url}>
                  <td className="mono url-cell" title={p.finalUrl !== p.url ? `跳到 ${p.finalUrl}` : p.url}>{p.url}</td>
                  <td className={"num " + (p.status >= 400 || p.error ? "st-bad" : "")}>{p.error || p.status || "—"}{p.noindex && <span className="chg new">noindex</span>}</td>
                  <td title={p.indexCoverage}>{p.indexVerdict === "" ? "—" : p.indexVerdict === "PASS" ? <span className="st-good">已收录</span> : <span className="st-warn">{p.indexCoverage || "未收录"}</span>}</td>
                  <td>{p.title}</td>
                  <td className="num mono">{p.lastOk ? fmtDate(p.lastOk) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {pages && pages.total > pages.pages.length && <p className="src-note">只显示前 {pages.pages.length} 个，用上面的筛选缩小范围。</p>}
        </section>
        {view && view.history.length > 0 && (
          <section className="panel">
            <h2>体检记录 <span className="mono">最近 {view.history.length} 次</span></h2>
            <table className="qtable">
              <thead><tr><th>时间</th><th>方式</th><th className="num">健康分</th><th className="num">P0 / P1 / P2</th><th className="num">页数</th><th>结果</th></tr></thead>
              <tbody>
                {view.history.map((c) => (
                  <tr key={c.id}>
                    <td className="mono">{fmtDateTime(c.startedAt)}</td>
                    <td>{c.origin === "manual" ? "手动" : "每日"}</td>
                    <td className="num">{c.status === "done" ? c.score : "—"}</td>
                    <td className="num">{c.status === "done" ? `${c.p0} / ${c.p1} / ${c.p2}` : "—"}</td>
                    <td className="num">{c.status === "done" ? c.pages : "—"}</td>
                    <td className={c.status === "failed" ? "st-bad" : ""}>{c.status === "running" ? "进行中" : c.status === "failed" ? c.error : "完成"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}
      </div>
    </>
  );
}
