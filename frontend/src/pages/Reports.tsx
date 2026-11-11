import { useCallback, useEffect, useState } from "react";
import { api, errText, onEvent } from "../api";
import { fmtDateTime } from "../format";
import type { Report, Site } from "../types";

/** 报告: weekly reports (every 7 days, after the daily check), reports on demand, export. */
export function ReportsPage({ site, openId }: { site: Site; openId?: number | null }) {
  const [list, setList] = useState<Report[]>([]);
  const [cur, setCur] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const open = useCallback((id: number) => {
    api.report(id).then(setCur, (e) => setErr(errText(e)));
  }, []);
  const load = useCallback((select?: number) => {
    api.reports(site.id).then((l) => {
      const items = l ?? [];
      setList(items);
      const id = select ?? items[0]?.id;
      if (id) open(id);
      else setCur(null);
    }, (e) => setErr(errText(e)));
  }, [site.id, open]);
  useEffect(() => {
    load(openId ?? undefined);
    return onEvent<{ siteId: number }>("reports:changed", (e) => { if (e.siteId === site.id) load(); });
  }, [site.id, openId, load]);

  const run = (fn: () => Promise<void>) => {
    setBusy(true);
    setErr("");
    setMsg("");
    fn().catch((e) => setErr(errText(e))).finally(() => setBusy(false));
  };
  const generate = () => run(async () => { const r = await api.generateReport(site.id); load(r.id); });
  const exportAs = (format: "html" | "md") => run(async () => {
    const path = await api.exportReport(cur!.id, format);
    if (path) setMsg("已保存到 " + path);
  });
  const remove = () => run(async () => { await api.deleteReport(cur!.id); load(); });

  return (
    <>
      <div className="toolbar">
        <span>每周自动生成一份（数据全部来自本机），也可以现在生成。</span>
        <span className="grow" />
        <button className="btn primary" disabled={busy} onClick={generate}>{busy ? "处理中…" : "现在生成"}</button>
      </div>
      <div className="content reports">
        {err && <p className="error kw-msg">{err}</p>}
        {msg && <p className="kw-msg st-good">{msg}</p>}
        {list.length === 0 ? (
          <div className="empty">还没有报告。每天 08:00 体检后，每 7 天自动生成一份周报；也可以点「现在生成」。</div>
        ) : (
          <>
            <section className="panel report-list">
              <h2>报告 <span className="mono">{list.length}</span></h2>
              {list.map((r) => (
                <button key={r.id} className={"report-item" + (cur?.id === r.id ? " on" : "")} onClick={() => open(r.id)}>
                  <b>{r.from} 至 {r.to}</b>
                  <small>{r.origin === "weekly" ? "周报" : "手动"} · {fmtDateTime(r.createdAt)}</small>
                </button>
              ))}
            </section>
            <section className="panel report-view">
              {cur && (
                <>
                  <div className="row report-actions">
                    <b className="grow">{cur.title}</b>
                    <button className="btn" disabled={busy} onClick={() => exportAs("html")}>导出 HTML</button>
                    <button className="btn" disabled={busy} onClick={() => exportAs("md")}>导出 Markdown</button>
                    <button className="btn" disabled={busy} onClick={remove}>删除</button>
                  </div>
                  {/* sandbox="" : no scripts, no navigation, no same-origin access; the report has none of these anyway. */}
                  <iframe className="report-frame" title={cur.title} sandbox="" srcDoc={cur.html} />
                </>
              )}
            </section>
          </>
        )}
      </div>
    </>
  );
}
