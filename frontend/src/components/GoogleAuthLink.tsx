import { useEffect, useState } from "react";
import { copyText, onEvent } from "../api";

/**
 * While a Google connect waits for the browser, shows the consent link
 * (GoogleService.Connect emits it as "google:auth-url") so it can be copied
 * into a browser on this computer that can reach Google.
 */
export function GoogleAuthLink({ active }: { active: boolean }) {
  const [url, setURL] = useState("");
  const [copied, setCopied] = useState(false);
  useEffect(() => onEvent<string>("google:auth-url", (u) => { setURL(u); setCopied(false); }), []);
  useEffect(() => { if (!active) setURL(""); }, [active]);
  if (!active || !url) return null;
  const copy = () => copyText(url).then(() => setCopied(true), () => setCopied(false));
  return (
    <div className="auth-link">
      <p className="hint">浏览器没有打开授权页面，或者打不开 Google？复制下面的链接，粘贴到这台电脑上能访问 Google 的浏览器里完成授权。</p>
      <div className="row">
        <input className="input mono" readOnly value={url} onFocus={(e) => e.currentTarget.select()} />
        <button className="btn" onClick={copy}>{copied ? "已复制" : "复制授权链接"}</button>
      </div>
    </div>
  );
}
