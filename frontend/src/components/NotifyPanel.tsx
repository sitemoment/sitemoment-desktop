import { useEffect, useState } from "react";
import { api, errText } from "../api";
import type { NotifySettings } from "../types";

/** Settings → 通知. */
export function NotifyPanel() {
  const [s, setS] = useState<NotifySettings | null>(null);
  const [msg, setMsg] = useState("");
  useEffect(() => {
    api.notifySettings().then(setS).catch(() => {});
  }, []);
  if (!s) return null;
  const toggle = async (on: boolean) => {
    try {
      setS(await api.setNativeNotify(on));
    } catch (e) {
      setMsg(errText(e));
    }
  };
  const test = async () => {
    setMsg("");
    try {
      await api.testNotify();
      setMsg("已发送测试通知。");
    } catch (e) {
      setMsg(errText(e));
    }
  };
  return (
    <section className="panel">
      <h2>通知</h2>
      <p>体检发现新的 P0 问题、体检失败、任务通过或到期未通过、采集器遇到验证码需要你处理、发现新版本时，应用里都有提示；窗口在后台时还可以用系统通知提醒。</p>
      <div className="row">
        <label className="check">
          <input type="checkbox" disabled={!s.available} checked={s.native && s.available} onChange={(e) => toggle(e.target.checked)} /> 窗口在后台时用系统通知
        </label>
        <button className="btn" disabled={!s.available || !s.native} onClick={test}>发送测试通知</button>
      </div>
      {!s.available && s.reason && <p className="hint">{s.reason}</p>}
      {msg && <p className="hint">{msg}</p>}
    </section>
  );
}
