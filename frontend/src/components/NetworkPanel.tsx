import { useEffect, useState } from "react";
import { api, errText } from "../api";
import { exceptionsText, probeText, proxySummary } from "../network";
import type { Probe, ProxyStatus } from "../types";

const toneClass = { good: "st-good", warn: "st-warn", plain: "" } as const;

/** Settings → 网络代理: the app follows the system proxy; this shows which and tests it. */
export function NetworkPanel() {
  const [s, setS] = useState<ProxyStatus | null>(null);
  const [probes, setProbes] = useState<Probe[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const refresh = () => api.proxyStatus().then(setS, (e) => setErr(errText(e)));
  useEffect(() => { refresh(); }, []);

  const test = async () => {
    setBusy(true);
    setErr("");
    setProbes([]);
    try {
      // Re-read the status too: the proxy may have been switched since the page opened.
      const [st, res] = await Promise.all([api.proxyStatus(), api.testNetwork()]);
      setS(st);
      setProbes(res);
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const sum = s && proxySummary(s);
  const ex = s ? exceptionsText(s) : "";
  return (
    <section className="panel">
      <h2>网络代理</h2>
      <p>SiteMoment 跟随系统代理（例如 Clash、V2Ray 的「设置为系统代理」）访问 Google 和 SiteMoment；系统没有代理时使用 HTTPS_PROXY 等环境变量。切换代理后半分钟内生效，不用重启。</p>
      {sum && <div className="row"><span className={toneClass[sum.tone]}>{sum.text}</span></div>}
      {ex && <p className="hint mono">{ex}</p>}
      <div className="row">
        <button className="btn" disabled={busy} onClick={test}>{busy ? "测试中…" : "测试连接"}</button>
      </div>
      {probes.map((p) => (
        <div className="row" key={p.url}><span className={p.ok ? "st-good" : "st-bad"}>● </span><span>{probeText(p)}</span></div>
      ))}
      {err && <p className="error">{err}</p>}
    </section>
  );
}
