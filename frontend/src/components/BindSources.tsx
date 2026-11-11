import { useEffect, useState } from "react";
import { api, errText } from "../api";
import { guessBingSite, guessGAProperty } from "../format";
import type { BingSite, Connection, GAProperty, Site } from "../types";

type Props = { site: Site; onChanged: () => void; onError: (msg: string) => void };

/** The site's data sources: Search Console (bound on its own page), GA4 and Bing. */
export function BindSources({ site, onChanged }: { site: Site; onChanged: () => void }) {
  const [err, setErr] = useState("");
  return (
    <section className="panel">
      <h2>数据源</h2>
      <div className="src-row">
        <span className="src-name">Search Console</span>
        <span className="mono">{site.gscProperty || "未绑定"}</span>
      </div>
      <BindGA site={site} onChanged={onChanged} onError={setErr} />
      <BindBing site={site} onChanged={onChanged} onError={setErr} />
      {err && <p className="error">{err}</p>}
      <p className="src-note">GA4 和 Bing 的数据也只保存在这台电脑上。绑定后立即开始同步：GA4 回填 16 个月，Bing 只提供最近几个月。</p>
    </section>
  );
}

function BindGA({ site, onChanged, onError }: Props) {
  const [editing, setEditing] = useState(false);
  const [conns, setConns] = useState<Connection[] | null>(null);
  const [account, setAccount] = useState("");
  const [props, setProps] = useState<GAProperty[] | null>(null);
  const [prop, setProp] = useState("");

  useEffect(() => {
    if (!editing) return;
    let live = true;
    api.connections().then((c) => {
      if (!live) return;
      setConns(c);
      setAccount(site.gaAccount || site.googleAccount || c[0]?.account || "");
    }).catch((e) => { if (live) onError(errText(e)); });
    return () => { live = false; };
  }, [editing, site.gaAccount, site.googleAccount, onError]);
  useEffect(() => {
    if (!editing || !account) return;
    let live = true;
    setProps(null);
    api.gaProperties(account)
      .then((p) => { if (live) { setProps(p); setProp(site.gaProperty || guessGAProperty(p, site.host)); } })
      .catch((e) => { if (live) { setProps([]); onError(errText(e)); } });
    return () => { live = false; };
  }, [editing, account, site.host, site.gaProperty, onError]);

  const save = async (acc: string, p: string) => {
    onError("");
    try {
      await api.bindGA(site.id, acc, p);
      setEditing(false);
      onChanged();
    } catch (e) {
      onError(errText(e));
    }
  };

  if (!editing) {
    return (
      <div className="src-row">
        <span className="src-name">GA4</span>
        <span className="mono">{site.gaProperty ? `${site.gaProperty} · ${site.gaAccount}` : "未绑定"}</span>
        <span className="grow" />
        <button className="btn" onClick={() => setEditing(true)}>{site.gaProperty ? "更换" : "绑定"}</button>
        {site.gaProperty && <button className="btn" onClick={() => save("", "")}>解绑</button>}
      </div>
    );
  }
  return (
    <div className="src-row">
      <span className="src-name">GA4</span>
      {conns !== null && conns.length === 0 ? (
        <span>先在「设置」里连接 Google 账号</span>
      ) : (
        <>
          <select value={account} onChange={(e) => setAccount(e.target.value)}>
            {(conns ?? []).map((c) => <option key={c.account} value={c.account}>{c.account}</option>)}
          </select>
          <select value={prop} onChange={(e) => setProp(e.target.value)} disabled={!props || props.length === 0}>
            {props === null && <option value="">读取媒体资源…</option>}
            {props?.length === 0 && <option value="">这个账号下没有可读的 GA4 媒体资源</option>}
            {props?.map((p) => (
              <option key={p.property} value={p.property}>{p.accountName} / {p.displayName}（{p.property.replace("properties/", "")}）</option>
            ))}
          </select>
        </>
      )}
      <span className="grow" />
      <button className="btn" onClick={() => setEditing(false)}>取消</button>
      <button className="btn primary" disabled={!account || !prop} onClick={() => save(account, prop)}>绑定并同步</button>
    </div>
  );
}

function BindBing({ site, onChanged, onError }: Props) {
  const [editing, setEditing] = useState(false);
  const [keys, setKeys] = useState<Connection[] | null>(null);
  const [account, setAccount] = useState("");
  const [sites, setSites] = useState<BingSite[] | null>(null);
  const [url, setUrl] = useState("");

  useEffect(() => {
    if (!editing) return;
    let live = true;
    api.bingKeys().then((k) => {
      if (!live) return;
      setKeys(k);
      setAccount(site.bingAccount || k[0]?.account || "");
    }).catch((e) => { if (live) onError(errText(e)); });
    return () => { live = false; };
  }, [editing, site.bingAccount, onError]);
  useEffect(() => {
    if (!editing || !account) return;
    let live = true;
    setSites(null);
    api.bingSites(account)
      .then((s) => { if (live) { setSites(s); setUrl(site.bingSite || guessBingSite(s, site.host) || s[0]?.url || ""); } })
      .catch((e) => { if (live) { setSites([]); onError(errText(e)); } });
    return () => { live = false; };
  }, [editing, account, site.host, site.bingSite, onError]);

  const save = async (acc: string, u: string) => {
    onError("");
    try {
      await api.bindBing(site.id, acc, u);
      setEditing(false);
      onChanged();
    } catch (e) {
      onError(errText(e));
    }
  };

  if (!editing) {
    return (
      <div className="src-row">
        <span className="src-name">Bing</span>
        <span className="mono">{site.bingSite ? `${site.bingSite} · ${site.bingAccount}` : "未绑定"}</span>
        <span className="grow" />
        <button className="btn" onClick={() => setEditing(true)}>{site.bingSite ? "更换" : "绑定"}</button>
        {site.bingSite && <button className="btn" onClick={() => save("", "")}>解绑</button>}
      </div>
    );
  }
  return (
    <div className="src-row">
      <span className="src-name">Bing</span>
      {keys !== null && keys.length === 0 ? (
        <span>先在「设置 → Bing Webmaster」里添加 API key</span>
      ) : (
        <>
          <select value={account} onChange={(e) => setAccount(e.target.value)}>
            {(keys ?? []).map((k) => <option key={k.account} value={k.account}>{k.account}</option>)}
          </select>
          <select value={url} onChange={(e) => setUrl(e.target.value)} disabled={!sites || sites.length === 0}>
            {sites === null && <option value="">读取站点…</option>}
            {sites?.length === 0 && <option value="">这个 key 下没有站点</option>}
            {sites?.map((s) => <option key={s.url} value={s.url}>{s.url}{s.verified ? "" : "（未验证）"}</option>)}
          </select>
        </>
      )}
      <span className="grow" />
      <button className="btn" onClick={() => setEditing(false)}>取消</button>
      <button className="btn primary" disabled={!account || !url} onClick={() => save(account, url)}>绑定并同步</button>
    </div>
  );
}
