import { CancelError } from "@wailsio/runtime";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, errText, isNetworkError, onEvent } from "./api";
import { AgentPanel } from "./agent/AgentPanel";
import { AddSite } from "./components/AddSite";
import { Notices } from "./components/Notices";
import { SitesPanel } from "./components/SitesPanel";
import { StatusBar } from "./components/StatusBar";
import { UpdateBar } from "./components/UpdateBar";
import { Toasts } from "./components/Toasts";
import { CollectorsPage } from "./pages/Collectors";
import { HealthPage } from "./pages/Health";
import { KeywordsPage } from "./pages/Keywords";
import { OverviewPage } from "./pages/Overview";
import { ReportsPage } from "./pages/Reports";
import { SettingsPage } from "./pages/Settings";
import { TasksPage } from "./pages/Tasks";
import { nextHumanRun, noticeTarget } from "./format";
import type { Account, CollectorStats, HealthStatus, Info, Notice, RunView, Site, SiteHealth, SyncProgress, SyncStatus } from "./types";

type Page = "overview" | "keywords" | "health" | "tasks" | "reports" | "collectors" | "settings";

/** Rail and tab entries for the per-site pages, in order. */
const sitePages: [Page, string, string][] = [["overview", "◧", "概览"], ["keywords", "⌕", "关键词"], ["health", "✚", "体检"], ["tasks", "☑", "任务"], ["reports", "▤", "报告"]];

export default function App() {
  const [account, setAccount] = useState<Account | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [siteId, setSiteId] = useState<number | null>(null);
  const [page, setPage] = useState<Page>("overview");
  const [statuses, setStatuses] = useState<Record<number, SyncStatus>>({});
  const [progress, setProgress] = useState<Record<number, SyncProgress>>({});
  const [info, setInfo] = useState<Info | null>(null);
  const [collect, setCollect] = useState<CollectorStats | null>(null);
  const [human, setHuman] = useState<RunView | null>(null);
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState("");
  const [loggingIn, setLoggingIn] = useState(false);
  const [modelRev, setModelRev] = useState(0);
  const [scores, setScores] = useState<Record<number, SiteHealth>>({});
  const [openReport, setOpenReport] = useState<number | null>(null);
  const [agentOpen, setAgentOpen] = useState(() => localStorage.getItem("agentOpen") !== "0");
  // Login can block up to 5 minutes if the user abandons the browser; keep the
  // cancellable promise around so the sidebar button can cancel it.
  const loginRef = useRef<ReturnType<typeof api.login> | null>(null);

  const reloadSites = useCallback(async () => {
    const list = await api.sites();
    setSites(list);
    setSiteId((id) => (id !== null && list.some((s) => s.id === id) ? id : list[0]?.id ?? null));
  }, []);

  useEffect(() => {
    // Local-first: the app works offline, so a network failure fetching the
    // account (the backend being unreachable) shouldn't show the red error
    // banner. Just keep whatever account state we had (unknown, on first
    // run) and show nothing.
    api.account().then(setAccount).catch((e) => {
      const msg = e instanceof Error ? e.message : String(e);
      if (!isNetworkError(msg)) setError(errText(e));
    });
    reloadSites().catch((e) => setError(errText(e)));
    api.info().then(setInfo);
    api.syncStatuses().then((list) => setStatuses(Object.fromEntries(list.map((s) => [s.siteId, s]))));
    const offStatus = onEvent<SyncStatus>("sync:status", (s) => {
      setStatuses((m) => ({ ...m, [s.siteId]: s }));
      if (s.state !== "running") {
        setProgress((m) => {
          if (!(s.siteId in m)) return m;
          const { [s.siteId]: _removed, ...rest } = m;
          return rest;
        });
        api.info().then(setInfo);
      }
    });
    const offProgress = onEvent<SyncProgress>("sync:progress", (p) => {
      setProgress((m) => ({ ...m, [p.siteId]: p }));
    });
    api.collectorStats().then(setCollect).catch(() => {});
    const offCollect = onEvent<CollectorStats>("collect:stats", setCollect);
    const offRun = onEvent<RunView>("collect:run", (r) => setHuman((cur) => nextHumanRun(cur, r)));
    const loadScores = () => api.healthScores().then((l) => setScores(Object.fromEntries((l ?? []).map((h) => [h.siteId, h])))).catch(() => {});
    loadScores();
    const offHealth = onEvent<HealthStatus>("health:status", (s) => { if (s.state !== "running") loadScores(); });
    return () => {
      offStatus();
      offProgress();
      offCollect();
      offRun();
      offHealth();
    };
  }, [reloadSites]);

  const login = () => {
    if (loginRef.current) {
      loginRef.current.cancel();
      return;
    }
    setError("");
    setLoggingIn(true);
    const p = api.login();
    loginRef.current = p;
    Promise.resolve(p).then(
      (a) => setAccount(a),
      (e) => {
        if (!(e instanceof CancelError)) setError(errText(e));
      },
    ).finally(() => {
      loginRef.current = null;
      setLoggingIn(false);
    });
  };

  const toggleAgent = () => setAgentOpen((o) => {
    localStorage.setItem("agentOpen", o ? "0" : "1");
    return !o;
  });
  const refreshAccount = useCallback(() => { api.account().then(setAccount).catch(() => {}); }, []);
  // A notice opens the page it is about, on its site.
  const openNotice = (n: Notice) => {
    const target = noticeTarget(n.ref);
    if (!target) return;
    if (n.siteId && sites.some((s) => s.id === n.siteId)) setSiteId(n.siteId);
    setOpenReport(target === "reports" ? Number(n.ref.split(":")[1]) : null);
    setPage(target);
  };

  const site = sites.find((s) => s.id === siteId) ?? null;
  return (
    <div className="window">
      <UpdateBar />
      <Toasts />
      <div className="titlebar">
        <span className="cmd">SiteMoment · {site?.host ?? "本地 SEO 运营"}</span>
        <button className={"agent-toggle" + (agentOpen ? " on" : "")} title={agentOpen ? "收起 Agent" : "打开 Agent"} onClick={toggleAgent}>Agent ◨</button>
      </div>
      <div className="body">
        <nav className="rail">
          {sitePages.map(([p, icon, label]) => (
            <button key={p} className={page === p ? "on" : ""} title={label} aria-label={label} onClick={() => setPage(p)}>{icon}</button>
          ))}
          <button className={page === "collectors" ? "on" : ""} title="采集器" aria-label="采集器" onClick={() => setPage("collectors")}>⧉</button>
          <button className={"bottom" + (page === "settings" ? " on" : "")} title="设置" aria-label="设置" onClick={() => setPage("settings")}>⚙</button>
        </nav>
        <SitesPanel sites={sites} selected={siteId} statuses={statuses} account={account}
          scores={scores}
          loggingIn={loggingIn}
          onSelect={(id) => { setSiteId(id); setOpenReport(null); setPage((p) => (p === "settings" || p === "collectors" ? "overview" : p)); }} onAdd={() => setAdding(true)} onLogin={login} />
        <main>
          <div className="tabs">
            {sitePages.map(([p, , label]) => (
              <span key={p} className={page === p ? "on" : ""}>{p === "overview" && site ? `概览 · ${site.host}` : label}</span>
            ))}
            <span className={page === "collectors" ? "on" : ""}>采集器</span>
            <span className={page === "settings" ? "on" : ""}>设置</span>
          </div>
          {error && <div className="toolbar error">{error}</div>}
          {human && (
            <div className="toolbar notice">
              <span className="grow">本机浏览器在查「{human.collector}」时遇到人工验证：请在弹出的窗口里完成，2 分钟内有效。</span>
              <button className="btn" onClick={() => api.revealRun(human.id).catch(() => setHuman(null))}>显示窗口</button>
            </div>
          )}
          {page === "collectors" ? (
            <CollectorsPage />
          ) : page === "settings" ? (
            <SettingsPage account={account} info={info} sites={sites} onAccount={setAccount} onLogin={login} onSitesChanged={reloadSites}
              onModelChanged={() => setModelRev((n) => n + 1)} />
          ) : !site ? (
            <div className="content"><div className="empty">还没有站点。<button className="btn primary" onClick={() => setAdding(true)}>添加第一个站点</button></div></div>
          ) : page === "keywords" ? (
            <KeywordsPage key={site.id} site={site} account={account} onCreditsUsed={refreshAccount} />
          ) : page === "health" ? (
            <HealthPage key={site.id} site={site} />
          ) : page === "tasks" ? (
            <TasksPage key={site.id} site={site} />
          ) : page === "reports" ? (
            <ReportsPage key={site.id} site={site} openId={openReport} />
          ) : (
            <OverviewPage key={site.id} site={site} status={statuses[site.id]} onChanged={reloadSites} onOpenHealth={() => setPage("health")} />
          )}
        </main>
        {agentOpen && <AgentPanel site={site} account={account} modelRev={modelRev} onOpenSettings={() => setPage("settings")} onCreditsUsed={refreshAccount} />}
      </div>
      <StatusBar status={site ? statuses[site.id] : undefined} progress={site ? progress[site.id] ?? null : null} info={info} siteId={site?.id} account={account} collect={collect}>
        <Notices onOpen={openNotice} />
      </StatusBar>
      {adding && (
        <AddSite onCancel={() => setAdding(false)} onDone={async (s) => { setAdding(false); await reloadSites(); setSiteId(s.id); setPage("overview"); }} />
      )}
    </div>
  );
}
