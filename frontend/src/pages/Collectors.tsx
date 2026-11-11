import { useCallback, useEffect, useState } from "react";
import { api, errText, onEvent } from "../api";
import { fmtTime } from "../format";
import type { CollectorView, RunView } from "../types";

const runState: Record<RunView["status"], [string, string]> = {
  running: ["进行中", ""], need_human: ["需要人工", "st-warn"], ok: ["完成", "st-good"], error: ["失败", "st-bad"], cancelled: ["已取消", ""],
};

function input(raw: string): string {
  try {
    const v = JSON.parse(raw) as Record<string, unknown>;
    return Object.entries(v).map(([k, x]) => `${k}=${String(x)}`).join(" · ");
  } catch {
    return raw;
  }
}

/** Collectors: built-in and user collectors, recent runs and the needs-human queue. */
export function CollectorsPage() {
  const [list, setList] = useState<CollectorView[]>([]);
  const [runs, setRuns] = useState<RunView[]>([]);
  const [human, setHuman] = useState<RunView[]>([]);
  const [dir, setDir] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const loadRuns = useCallback(() => {
    api.collectorRuns(50).then(setRuns).catch(() => {});
    api.needsHuman().then(setHuman).catch(() => {});
  }, []);
  useEffect(() => {
    api.collectors().then(setList, (e) => setErr(errText(e)));
    api.collectorDir().then(setDir).catch(() => {});
    loadRuns();
    return onEvent<RunView>("collect:run", loadRuns);
  }, [loadRuns]);

  const guard = (fn: () => Promise<void>) => async () => {
    setBusy(true);
    setErr("");
    try { await fn(); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  };
  const rescan = guard(async () => setList(await api.rescanCollectors()));
  // version, hosts and sha must be exactly what this row shows: Catalog.Approve
  // checks all three against what is actually pending.
  const approve = (c: CollectorView) => guard(async () => setList(await api.approveCollector(c.id, c.version, c.hosts, c.sha)))();
  const reveal = (id: number) => guard(async () => api.revealRun(id))();

  return (
    <div className="content">
      {err && <p className="error kw-msg">{err}</p>}
      {human.length > 0 && (
        <section className="panel">
          <h2>需要人工处理 <span>{human.length}</span></h2>
          <p>网站要求人机验证。点「显示窗口」，在弹出的窗口里完成验证，采集会自动继续；2 分钟内没处理就会失败。</p>
          {human.map((r) => (
            <div className="row" key={r.id}>
              <span className="mono">{r.collector}</span><span>{input(r.input)}</span><span className="grow" />
              <button className="btn primary" disabled={busy} onClick={() => reveal(r.id)}>显示窗口</button>
            </div>
          ))}
        </section>
      )}
      <section className="panel">
        <h2>采集器 <span>{list.length}</span></h2>
        <table className="qtable">
          <thead><tr><th>名称</th><th>Agent 工具</th><th>访问的域名</th><th>限速 / 缓存</th><th>状态</th></tr></thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id}>
                <td>{c.name} {c.builtin ? <span className="tag tag-found">内置</span> : <span className="tag tag-scene">用户</span>}<div className="site-note mono">{c.id} · v{c.version}</div></td>
                <td className="mono">{c.tool}</td>
                <td className="mono">{c.hosts.join(", ")}</td>
                <td className="mono">每天 {c.dailyLimit} 次 · 间隔 ≥ {c.minGapSeconds} 秒 · 缓存 {c.cacheHours} 小时</td>
                <td>
                  {c.status === "enabled" && <span className="st-good">● 可用</span>}
                  {c.status === "needs_approval" && (
                    <button className="btn primary" disabled={busy} title={`允许它在隐藏窗口里打开：${c.hosts.join(", ")}`} onClick={() => approve(c)}>允许访问这些域名</button>
                  )}
                  {c.status === "invalid" && <span className="st-bad" title={c.error}>■ 无效：{c.error}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="row">
          <button className="btn" disabled={busy} onClick={rescan}>重新扫描</button>
          <span className="src-note">自己的采集器：把采集器目录（manifest.json、schema.json、collector.js）放进 <span className="mono">{dir}</span>，然后点「重新扫描」。第一次使用前要确认它会访问的域名。</span>
        </div>
      </section>
      <section className="panel">
        <h2>最近运行 <span>{runs.length}</span></h2>
        <table className="qtable">
          <thead><tr><th>时间</th><th>采集器</th><th>输入</th><th>状态</th></tr></thead>
          <tbody>
            {runs.map((r) => {
              const [label, cls] = runState[r.status] ?? [r.status, ""];
              return (
                <tr key={r.id}>
                  <td className="mono">{fmtTime(r.createdAt)}</td>
                  <td className="mono">{r.collector}</td>
                  <td>{input(r.input)}</td>
                  <td className={cls} title={r.error}>{label}{r.error ? `：${r.error}` : ""}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {runs.length === 0 && <p className="src-note">还没有运行记录。Agent 查谷歌搜索结果、Google Trends 时会用到采集器。</p>}
      </section>
    </div>
  );
}
