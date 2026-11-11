import { useEffect, useState } from "react";
import { api, errText } from "../api";
import type { Account, ModelSettings as Settings, Provider } from "../types";

const providers: { id: Provider; name: string; note: string }[] = [
  { id: "hosted", name: "SiteMoment 托管", note: "需要登录，按 token 扣本月积分（每 1,000 token 1 积分）" },
  { id: "openai", name: "OpenAI 兼容接口", note: "自己的 base URL、key 和模型名（OpenAI、DeepSeek、本地 vLLM 等）" },
  { id: "anthropic", name: "Anthropic", note: "自己的 Anthropic API key" },
];

/** Settings → AI 模型: which model the agent uses, and the user's own keys. */
export function ModelSettingsPanel({ account, onChanged }: { account: Account | null; onChanged: () => void }) {
  const [s, setS] = useState<Settings | null>(null);
  const [hosted, setHosted] = useState<string[]>([]);
  const [keys, setKeys] = useState({ openai: "", anthropic: "" });
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    api.modelSettings().then(setS, (e) => setErr(errText(e)));
  }, []);
  useEffect(() => {
    if (account?.signedIn) api.hostedModels().then(setHosted, () => setHosted([]));
    else setHosted([]);
  }, [account?.signedIn]);

  const guard = (fn: () => Promise<void>) => async () => {
    setBusy(true);
    setErr("");
    setMsg("");
    try { await fn(); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  };
  const save = guard(async () => {
    setS(await api.saveModelSettings(s!));
    setMsg("已保存");
    onChanged();
  });
  const setKey = (provider: "openai" | "anthropic", key: string) => guard(async () => {
    setS(await api.setModelKey(provider, key));
    setKeys((k) => ({ ...k, [provider]: "" }));
    setMsg(key ? "key 已保存到系统钥匙串" : "key 已删除");
    onChanged();
  })();
  const test = guard(async () => { setMsg(await api.testModel()); });

  if (!s) return <section className="panel"><h2>AI 模型</h2>{err && <p className="error">{err}</p>}</section>;
  const field = (k: keyof Settings) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: e.target.value });
  const keyRow = (provider: "openai" | "anthropic", set: boolean, placeholder: string) => (
    <div className="row">
      {set ? (
        <><span className="st-good">● key 已保存</span><button className="btn" disabled={busy} onClick={() => setKey(provider, "")}>删除</button></>
      ) : (
        <>
          <input className="input" type="password" placeholder={placeholder} value={keys[provider]} onChange={(e) => setKeys({ ...keys, [provider]: e.target.value })} />
          <button className="btn" disabled={busy || !keys[provider].trim()} onClick={() => setKey(provider, keys[provider].trim())}>保存 key</button>
        </>
      )}
    </div>
  );

  return (
    <section className="panel model-settings">
      <h2>AI 模型 <span>{s.label}</span></h2>
      {providers.map((p) => (
        <label key={p.id} className="radio-row">
          <input type="radio" name="provider" checked={s.provider === p.id} onChange={() => setS({ ...s, provider: p.id })} />
          <span><b>{p.name}</b><small>{p.note}</small></span>
        </label>
      ))}
      {s.provider === "hosted" && (
        <div className="row">
          <span className="src-name">模型</span>
          {account?.signedIn ? (
            <select value={s.hostedModel} onChange={field("hostedModel")}>
              <option value="">默认</option>
              {hosted.map((m) => <option key={m} value={m}>{m}</option>)}
            </select>
          ) : <span className="st-warn">登录 SiteMoment 后可用</span>}
        </div>
      )}
      {s.provider === "openai" && (
        <>
          <div className="row"><span className="src-name">Base URL</span><input className="input" value={s.openaiBaseUrl} placeholder="https://api.openai.com/v1" onChange={field("openaiBaseUrl")} /></div>
          <div className="row"><span className="src-name">模型</span><input className="input" value={s.openaiModel} placeholder="例如 gpt-5.6、deepseek-chat" onChange={field("openaiModel")} /></div>
          {keyRow("openai", s.openaiKeySet, "API key")}
        </>
      )}
      {s.provider === "anthropic" && (
        <>
          <div className="row"><span className="src-name">模型</span><input className="input" value={s.anthropicModel} placeholder="claude-opus-5" onChange={field("anthropicModel")} /></div>
          {keyRow("anthropic", s.anthropicKeySet, "Anthropic API key（sk-ant-…）")}
        </>
      )}
      <div className="row">
        <button className="btn primary" disabled={busy} onClick={save}>保存</button>
        <button className="btn" disabled={busy} onClick={test}>{busy ? "…" : "测试连接"}</button>
        {msg && <span className="st-good">{msg}</span>}
      </div>
      {err && <p className="error">{err}</p>}
      <p className="src-note">
        隐私：用托管模型时，发给模型的内容（包括你的 GSC、GA4、Bing 数据）会经过 SiteMoment 的服务器转发给模型，只转发、不存储，服务器只记录用量。
        用自己的 key 时，内容直接发给你选的模型服务商。调用 SiteMoment 付费数据时，只发送关键词和域名，不发送 GSC 或 GA 的数据。key 只保存在系统钥匙串里。
      </p>
    </section>
  );
}
