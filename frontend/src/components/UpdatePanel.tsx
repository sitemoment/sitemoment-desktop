import { updateView } from "../updates";
import { useUpdates } from "../useUpdates";

const LABEL = { check: "检查更新", install: "下载并安装", restart: "重启并更新" } as const;

const day = (unix: number) => new Date(unix * 1000).toLocaleString("zh-CN", { dateStyle: "short", timeStyle: "short" });

/** Settings → 软件更新. */
export function UpdatePanel() {
  const { status, err, act, setAuto } = useUpdates();
  if (!status) return null;
  const v = updateView(status);
  const off = status.state === "off";
  return (
    <section className="panel">
      <h2>软件更新</h2>
      <dl>
        <dt>当前版本</dt><dd>v{status.current}</dd>
        <dt>状态</dt><dd>{v.busy && <span className="spin" />} {v.text}</dd>
        {status.checkedAt > 0 && (<><dt>上次检查</dt><dd>{day(status.checkedAt)}</dd></>)}
      </dl>
      {err && <p className="error">{err}</p>}
      {status.latest?.notes && (status.state === "available" || status.state === "ready") && (
        <details className="notes"><summary>v{status.latest.version} 更新说明</summary><pre>{status.latest.notes}</pre></details>
      )}
      <div className="row">
        {v.action && v.action !== "check" && <button className="btn primary" onClick={() => act(v.action!)}>{LABEL[v.action]}</button>}
        <button className="btn" disabled={off || v.busy || status.state === "ready"} onClick={() => act("check")}>检查更新</button>
        <label className="check">
          <input type="checkbox" disabled={off} checked={status.auto} onChange={(e) => setAuto(e.target.checked)} /> 每天自动检查（从 GitHub Releases）
        </label>
      </div>
      <p className="hint">更新包从 GitHub Releases 下载，安装前核对 SHA-256（发布了签名公钥的版本还核对签名）；重启时只替换应用本身，本机数据不受影响。</p>
    </section>
  );
}
