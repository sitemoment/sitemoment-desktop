import { useState } from "react";
import { api, errText } from "../api";
import type { Site } from "../types";

export function AddSite({ onDone, onCancel }: { onDone: (s: Site) => void; onCancel: () => void }) {
  const [host, setHost] = useState("");
  const [err, setErr] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    try {
      onDone(await api.addSite(host));
    } catch (e) {
      setErr(errText(e).includes("valid site host") ? "请输入网站域名，例如 example.com" : errText(e));
      setSubmitting(false);
    }
  };
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };
  return (
    <div className="modal" onClick={onCancel} onKeyDown={onKeyDown}>
      <form className="modal-box" onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <h3>添加站点</h3>
        <input className="input" autoFocus placeholder="example.com" value={host} onChange={(e) => setHost(e.target.value)} />
        {err && <p className="error">{err}</p>}
        <div className="row">
          <span style={{ flex: 1 }} />
          <button type="button" className="btn" onClick={onCancel}>取消</button>
          <button className="btn primary" disabled={!host.trim() || submitting}>添加</button>
        </div>
      </form>
    </div>
  );
}
