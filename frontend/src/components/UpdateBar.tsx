import { useState } from "react";
import { openURL } from "../api";
import { updateBarVisible, updateView } from "../updates";
import { useUpdates } from "../useUpdates";

const LABEL = { check: "检查更新", install: "下载并安装", restart: "重启并更新" } as const;

/** A small bar under the title bar while a new version is available, downloading or ready. */
export function UpdateBar() {
  const { status, err, act } = useUpdates();
  const [hidden, setHidden] = useState("");
  if (!status || !updateBarVisible(status)) return null;
  const key = `${status.latest?.version}:${status.state}`;
  if (hidden === key) return null;
  const v = updateView(status);
  return (
    <div className="update-bar" role="status">
      {v.busy && <span className="spin" />}
      <span>{err || v.text}</span>
      {status.latest?.url && <button className="link" onClick={() => openURL(status.latest!.url)}>更新说明</button>}
      {v.action && <button className="btn primary" onClick={() => act(v.action!)}>{LABEL[v.action]}</button>}
      {!v.busy && <button className="link" aria-label="稍后" onClick={() => setHidden(key)}>稍后</button>}
    </div>
  );
}
