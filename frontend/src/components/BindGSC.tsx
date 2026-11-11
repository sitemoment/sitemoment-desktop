import { CancelError } from "@wailsio/runtime";
import { useEffect, useRef, useState } from "react";
import { api, errText, type Cancellable } from "../api";
import { guessProperty } from "../format";
import type { Connection, Property, Site } from "../types";

export function BindGSC({ site, onBound, children }: { site: Site; onBound: () => void; children?: React.ReactNode }) {
  const [configured, setConfigured] = useState(true);
  const [conns, setConns] = useState<Connection[]>([]);
  const [account, setAccount] = useState("");
  const [props, setProps] = useState<Property[]>([]);
  const [prop, setProp] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  // Connect can block up to 5 minutes if the user abandons the browser; keep
  // the cancellable promise around so the button can cancel it instead of
  // waiting it out (see App.tsx's login for the same pattern).
  const connectRef = useRef<Cancellable<Connection> | null>(null);

  useEffect(() => {
    api.googleConfigured().then(setConfigured);
    api.connections().then((c) => { setConns(c); if (c[0]) setAccount(c[0].account); });
  }, []);
  useEffect(() => {
    if (!account) return;
    let live = true;
    setProps([]);
    setErr("");
    api.properties(account)
      .then((p) => { if (live) { setProps(p); setProp(guessProperty(p, site.host)); } })
      .catch((e) => { if (live) setErr(errText(e)); });
    return () => { live = false; };
  }, [account, site.host]);

  const connect = () => {
    if (connectRef.current) {
      connectRef.current.cancel();
      return;
    }
    setErr("");
    setBusy(true);
    const p = api.connectGoogle();
    connectRef.current = p;
    Promise.resolve(p).then(
      (c) => {
        setConns((cs) => [...cs.filter((x) => x.account !== c.account), c]);
        setAccount(c.account);
      },
      (e) => {
        if (!(e instanceof CancelError)) setErr(errText(e));
      },
    ).finally(() => {
      connectRef.current = null;
      setBusy(false);
    });
  };
  const bind = async () => {
    try {
      await api.bindGSC(site.id, account, prop);
      onBound();
    } catch (e) {
      setErr(errText(e));
    }
  };

  return (
    <div className="content">
      <section className="panel">
        <h2>连接 Search Console</h2>
        <p>选择能读取 <b>{site.host}</b> 的 Google 账号和资源。数据只保存在这台电脑上，Google 授权放在系统钥匙串里。</p>
        {!configured && <p className="error">这个版本没有配置 Google OAuth client ID。开发时请设置环境变量 SITEMOMENT_GOOGLE_CLIENT_ID 和 SITEMOMENT_GOOGLE_CLIENT_SECRET。</p>}
        <div className="row">
          {conns.length > 0 && (
            <select value={account} onChange={(e) => setAccount(e.target.value)}>
              {conns.map((c) => <option key={c.account} value={c.account}>{c.account}</option>)}
            </select>
          )}
          <button className="btn" onClick={connect} disabled={!configured}>{busy ? "等待浏览器里完成授权…（取消）" : "连接 Google 账号"}</button>
        </div>
        {account && (
          <div className="row">
            <select value={prop} onChange={(e) => setProp(e.target.value)} disabled={props.length === 0}>
              {props.length === 0 && <option>读取资源列表…</option>}
              {props.map((p) => <option key={p.siteUrl} value={p.siteUrl}>{p.siteUrl}</option>)}
            </select>
            <button className="btn primary" disabled={!prop} onClick={bind}>绑定并开始同步</button>
          </div>
        )}
        {err && <p className="error">{err}</p>}
      </section>
      {children}
    </div>
  );
}
