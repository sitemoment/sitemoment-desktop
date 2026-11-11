import { CancelError } from "@wailsio/runtime";
import { useEffect, useRef, useState } from "react";
import { api, errText, signedOut, type Cancellable } from "../api";
import { fmtBytes } from "../format";
import type { Account, Connection, Info, Site } from "../types";

// WKWebView (Wails' macOS webview) has no WKUIDelegate hookup for the JS
// confirm() panel, so window.confirm() never shows a dialog and just
// resolves falsy — removal would silently no-op. Use an inline two-click
// confirm instead: the button asks again for a few seconds before it acts.
const CONFIRM_MS = 3000;

export function SettingsPage(props: {
  account: Account | null; info: Info | null; sites: Site[];
  onAccount: (a: Account) => void; onLogin: () => void; onSitesChanged: () => void;
}) {
  const { account, info, sites } = props;
  const [conns, setConns] = useState<Connection[]>([]);
  const [configured, setConfigured] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [confirmId, setConfirmId] = useState<number | null>(null);
  // Connect can block up to 5 minutes if the user abandons the browser; keep
  // the cancellable promise around so the button can cancel it (see
  // App.tsx's login and BindGSC for the same pattern).
  const connectRef = useRef<Cancellable<Connection> | null>(null);
  const confirmTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    api.connections().then(setConns);
    api.googleConfigured().then(setConfigured);
    return () => { if (confirmTimer.current) clearTimeout(confirmTimer.current); };
  }, []);

  const guard = (fn: () => Promise<void>) => async () => {
    setBusy(true);
    setErr("");
    try { await fn(); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  };
  const logout = guard(async () => { await api.logout(); props.onAccount(signedOut); });
  const connect = () => {
    if (connectRef.current) {
      connectRef.current.cancel();
      return;
    }
    setErr("");
    setConnecting(true);
    const p = api.connectGoogle();
    connectRef.current = p;
    Promise.resolve(p).then(
      async () => setConns(await api.connections()),
      (e) => {
        if (!(e instanceof CancelError)) setErr(errText(e));
      },
    ).finally(() => {
      connectRef.current = null;
      setConnecting(false);
    });
  };
  const disconnect = (a: string) => guard(async () => { await api.disconnectGoogle(a); setConns(await api.connections()); })();
  const remove = (s: Site) => {
    if (confirmId !== s.id) {
      setConfirmId(s.id);
      if (confirmTimer.current) clearTimeout(confirmTimer.current);
      confirmTimer.current = setTimeout(() => setConfirmId(null), CONFIRM_MS);
      return;
    }
    if (confirmTimer.current) clearTimeout(confirmTimer.current);
    setConfirmId(null);
    guard(async () => {
      await api.removeSite(s.id);
      props.onSitesChanged();
    })();
  };

  return (
    <div className="content settings">
      {err && <p className="error">{err}</p>}
      <section className="panel">
        <h2>SiteMoment 账号</h2>
        {account?.signedIn ? (
          <div className="row"><span>{account.email} · {account.plan || "free"}</span><button className="btn" disabled={busy} onClick={logout}>退出登录</button></div>
        ) : (
          <>
            <p>登录后可以使用托管模型和 SiteMoment 的 SEO 数据（后续版本）。站点数据和 Google 授权始终只保存在这台电脑上。</p>
            <button className="btn primary" onClick={props.onLogin}>登录 SiteMoment</button>
          </>
        )}
      </section>
      <section className="panel">
        <h2>Google 账号</h2>
        {conns.map((c) => (
          <div className="row" key={c.account}><span className="mono">{c.account}</span><button className="btn" disabled={busy} onClick={() => disconnect(c.account)}>断开</button></div>
        ))}
        {!configured && <p className="error">这个版本没有配置 Google OAuth client ID。</p>}
        <div className="row"><button className="btn" disabled={!configured} onClick={connect}>{connecting ? "等待浏览器里完成授权…（取消）" : "连接 Google 账号"}</button></div>
      </section>
      <section className="panel">
        <h2>站点</h2>
        {sites.map((s) => (
          <div className="row" key={s.id}>
            <span className="mono" style={{ minWidth: 220 }}>{s.host}</span>
            <span>{s.gscProperty || "未绑定"}</span>
            <span style={{ flex: 1 }} />
            <button className="btn" disabled={busy} onClick={() => remove(s)}>{confirmId === s.id ? "再点一次确认删除" : "删除"}</button>
          </div>
        ))}
      </section>
      <section className="panel">
        <h2>本机数据</h2>
        <dl>
          <dt>数据目录</dt><dd>{info?.dataDir}</dd>
          <dt>数据库大小</dt><dd>{info ? fmtBytes(info.dbBytes) : ""}</dd>
          <dt>版本</dt><dd>{info?.version}</dd>
        </dl>
      </section>
    </div>
  );
}
