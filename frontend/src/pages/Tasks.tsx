import { useCallback, useEffect, useState } from "react";
import { api, errText, onEvent } from "../api";
import { fmtDate, fmtDateTime, outcomeText, taskStatusLabel } from "../format";
import type { Site, TaskStatus, TaskView } from "../types";

const sections: [string, TaskStatus[]][] = [["待确认（Agent 建议）", ["proposed"]], ["进行中", ["open"]], ["已结束", ["passed", "failed"]]];
const stateClass = { pass: "st-good", unmet: "st-warn", pending: "" } as const;

function TaskCard({ t, busy, act }: { t: TaskView; busy: boolean; act: (fn: () => Promise<unknown>) => void }) {
  return (
    <li className="task">
      <div className="task-head">
        <span className={"task-state ts-" + t.status}>{taskStatusLabel[t.status]}</span>
        <b className="grow">{t.title}</b>
        {t.status === "open" && <span className="mono task-due">到期 {fmtDate(t.dueAt)}</span>}
        {t.doneAt > 0 && t.status !== "dismissed" && <span className="mono task-due">{fmtDate(t.doneAt)}</span>}
      </div>
      {t.detail && <p className="task-detail">{t.detail}</p>}
      <ul className="task-conds">
        {t.labels.map((l, i) => {
          const o = t.outcomes[i];
          return (
            <li key={i}>
              <span>验收：{l}</span>
              {t.status !== "proposed" && <span className={o ? stateClass[o.state] ?? "" : ""}>{outcomeText(o)}</span>}
              {o?.note && <small>{o.note}</small>}
            </li>
          );
        })}
      </ul>
      <div className="row">
        {t.status === "proposed" && (
          <>
            <button className="btn primary" disabled={busy} onClick={() => act(() => api.confirmTask(t.id))}>确认，开始计时（{t.days} 天）</button>
            <button className="btn" disabled={busy} onClick={() => act(() => api.dismissTask(t.id))}>忽略</button>
          </>
        )}
        {t.status === "open" && (
          <>
            <button className="btn" disabled={busy} onClick={() => act(() => api.checkTask(t.id))}>立即复查</button>
            <button className="btn" disabled={busy} onClick={() => act(() => api.dismissTask(t.id))}>放弃</button>
            {t.checkedAt > 0 && <span className="mono task-due">上次复查 {fmtDateTime(t.checkedAt)}</span>}
          </>
        )}
        {(t.status === "passed" || t.status === "failed") && (
          <button className="btn" disabled={busy} onClick={() => act(() => api.deleteTask(t.id))}>删除</button>
        )}
      </div>
    </li>
  );
}

/** 任务: tasks with acceptance conditions, checked after every sync and health check. */
export function TasksPage({ site }: { site: Site }) {
  const [list, setList] = useState<TaskView[]>([]);
  const [auto, setAuto] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const load = useCallback(() => {
    api.tasks(site.id).then((l) => setList(l ?? []), (e) => setErr(errText(e)));
  }, [site.id]);
  useEffect(() => {
    load();
    api.autoAcceptTasks().then(setAuto).catch(() => {});
    return onEvent<{ siteId: number }>("tasks:changed", (e) => { if (e.siteId === site.id) load(); });
  }, [site.id, load]);
  const act = (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr("");
    fn().then(load, (e) => setErr(errText(e))).finally(() => setBusy(false));
  };
  const toggleAuto = () => api.setAutoAcceptTasks(!auto).then(() => setAuto(!auto), (e) => setErr(errText(e)));

  return (
    <>
      <div className="toolbar">
        <span>任务由 Agent 建议，带可以自动判断的验收条件；每次同步和体检后自动复查。</span>
        <span className="grow" />
        <label className="check"><input type="checkbox" checked={auto} onChange={toggleAuto} /> Agent 建议的任务直接开始</label>
      </div>
      <div className="content">
        {err && <p className="error kw-msg">{err}</p>}
        {list.filter((t) => t.status !== "dismissed").length === 0 && (
          <div className="empty">还没有任务。在右侧问 Agent，比如「这个词怎么进前 10？」，它会把建议变成带验收条件的任务。</div>
        )}
        {sections.map(([title, statuses]) => {
          const items = list.filter((t) => statuses.includes(t.status));
          if (items.length === 0) return null;
          return (
            <section className="panel" key={title}>
              <h2>{title} <span className="mono">{items.length}</span></h2>
              <ul className="task-list">{items.map((t) => <TaskCard key={t.id} t={t} busy={busy} act={act} />)}</ul>
            </section>
          );
        })}
      </div>
    </>
  );
}
