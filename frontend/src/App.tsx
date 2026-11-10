import { CancelError } from "@wailsio/runtime";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, errText, isNetworkError, onEvent } from "./api";
import { AddSite } from "./components/AddSite";
import { SitesPanel } from "./components/SitesPanel";
import { StatusBar } from "./components/StatusBar";
import { OverviewPage } from "./pages/Overview";
import { SettingsPage } from "./pages/Settings";
import type { Account, Info, Site, SyncProgress, SyncStatus } from "./types";

type Page = "overview" | "settings";

export default function App() {
  const [account, setAccount] = useState<Account | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [siteId, setSiteId] = useState<number | null>(null);
  const [page, setPage] = useState<Page>("overview");
  const [statuses, setStatuses] = useState<Record<number, SyncStatus>>({});
  const [progress, setProgress] = useState<SyncProgress | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState("");
  const [loggingIn, setLoggingIn] = useState(false);
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
        setProgress(null);
        api.info().then(setInfo);
      }
    });
    const offProgress = onEvent<SyncProgress>("sync:progress", setProgress);
    return () => {
      offStatus();
      offProgress();
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

  const site = sites.find((s) => s.id === siteId) ?? null;
  return (
    <div className="window">
      <div className="titlebar"><span className="cmd">SiteMoment · {site?.host ?? "本地 SEO 运营"}</span></div>
      <div className="body">
        <nav className="rail">
          <button className={page === "overview" ? "on" : ""} title="概览" aria-label="概览" onClick={() => setPage("overview")}>◧</button>
          <button className={"bottom" + (page === "settings" ? " on" : "")} title="设置" aria-label="设置" onClick={() => setPage("settings")}>⚙</button>
        </nav>
        <SitesPanel sites={sites} selected={siteId} statuses={statuses} account={account} loggingIn={loggingIn}
          onSelect={(id) => { setSiteId(id); setPage("overview"); }} onAdd={() => setAdding(true)} onLogin={login} />
        <main>
          <div className="tabs">
            <span className={page === "overview" ? "on" : ""}>{site ? `概览 · ${site.host}` : "概览"}</span>
            <span className={page === "settings" ? "on" : ""}>设置</span>
          </div>
          {error && <div className="toolbar error">{error}</div>}
          {page === "settings" ? (
            <SettingsPage account={account} info={info} sites={sites} onAccount={setAccount} onLogin={login} onSitesChanged={reloadSites} />
          ) : site ? (
            <OverviewPage key={site.id} site={site} status={statuses[site.id]} onChanged={reloadSites} />
          ) : (
            <div className="content"><div className="empty">还没有站点。<button className="btn primary" onClick={() => setAdding(true)}>添加第一个站点</button></div></div>
          )}
        </main>
      </div>
      <StatusBar status={site ? statuses[site.id] : undefined} progress={progress} info={info} siteId={site?.id} />
      {adding && (
        <AddSite onCancel={() => setAdding(false)} onDone={async (s) => { setAdding(false); await reloadSites(); setSiteId(s.id); setPage("overview"); }} />
      )}
    </div>
  );
}
