import { useCallback, useEffect, useState } from "react";
import { api, errText } from "../api";
import { fmtCredits, fmtInt, kdClass, kindLabel, metricNote } from "../format";
import type { Account, CloudQuote, Keyword, KeywordKind, KeywordList, Site } from "../types";

const FILTERS: { kind: "" | KeywordKind; label: string }[] = [
  { kind: "", label: "全部" },
  { kind: "target", label: "目标词" },
  { kind: "found", label: "意外流量" },
  { kind: "scene", label: "场景词" },
];
const KINDS: KeywordKind[] = ["target", "found", "scene"];

export function KeywordsPage({ site, account, onCreditsUsed }: { site: Site; account: Account | null; onCreditsUsed: () => void }) {
  const [kind, setKind] = useState<"" | KeywordKind>("");
  const [list, setList] = useState<KeywordList | null>(null);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [text, setText] = useState("");
  const [addKind, setAddKind] = useState<KeywordKind>("target");
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");
  const [err, setErr] = useState("");
  const [quote, setQuote] = useState<CloudQuote | null>(null);

  const reload = useCallback(async () => {
    try {
      setList(await api.keywords(site.id, kind));
    } catch (e) {
      setErr(errText(e));
    }
  }, [site.id, kind]);
  useEffect(() => {
    setSelected(new Set());
    reload();
  }, [reload]);

  const rows = list?.keywords ?? [];
  const allOn = rows.length > 0 && rows.every((r) => selected.has(r.id));
  const toggle = (id: number) => setSelected((s) => {
    const n = new Set(s);
    if (n.has(id)) n.delete(id); else n.add(id);
    return n;
  });
  const toggleAll = () => setSelected(allOn ? new Set() : new Set(rows.map((r) => r.id)));
  const ids = () => [...selected];

  const run = async (what: string, fn: () => Promise<void>) => {
    setBusy(what);
    setErr("");
    setNote("");
    try {
      await fn();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy("");
    }
  };
  const add = () => run("add", async () => {
    const n = await api.addKeywords(site.id, text, addKind);
    setText("");
    setNote(`已添加 ${n} 个词`);
    await reload();
  });
  const fetchBing = () => run("bing", async () => {
    const r = await api.fetchBing(site.id, ids());
    setNote(`Bing：更新 ${r.updated} 个词${r.skipped ? `，${r.skipped} 个没有数据或已有 SiteMoment 数据` : ""}`);
    await reload();
  });
  const askCloud = () => run("quote", async () => setQuote(await api.quoteCloud(site.id, ids())));
  const fetchCloud = () => run("cloud", async () => {
    setQuote(null);
    const r = await api.fetchCloud(site.id, ids());
    setNote(`SiteMoment：更新 ${r.updated} 个词${r.skipped ? `，${r.skipped} 个没有数据` : ""}，扣 ${fmtCredits(r.credits)} 积分${r.cached ? "（缓存命中）" : ""}`);
    onCreditsUsed();
    await reload();
  });
  const setKindOf = (k: Keyword, v: KeywordKind) => run("edit", async () => {
    await api.updateKeyword(k.id, v, k.notes);
    await reload();
  });
  const saveNotes = (k: Keyword, notes: string) => {
    if (notes !== k.notes) run("edit", async () => { await api.updateKeyword(k.id, k.kind, notes); await reload(); });
  };
  const remove = (k: Keyword) => run("edit", async () => {
    await api.removeKeyword(k.id);
    setSelected((s) => { const n = new Set(s); n.delete(k.id); return n; });
    await reload();
  });

  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {FILTERS.map((f) => <button key={f.label} className={f.kind === kind ? "on" : ""} onClick={() => setKind(f.kind)}>{f.label}</button>)}
        </div>
        <span className="mono">{list?.to ? `GSC ${list.from} 至 ${list.to}` : "还没有 Search Console 数据"}</span>
        <span className="grow" />
        <span className="mono">已选 {selected.size}</span>
        <button className="btn" disabled={!selected.size || !!busy} onClick={fetchBing}>{busy === "bing" ? "Bing 取数中…" : "取数 · Bing（免费）"}</button>
        <button className="btn primary" disabled={!selected.size || !!busy} onClick={askCloud}>
          {busy === "quote" || busy === "cloud" ? "SiteMoment 取数中…" : "取数 · SiteMoment"}
        </button>
      </div>
      <div className="content">
        <section className="panel">
          <h2>添加关键词 <span className="mono">每行一个，也可以用逗号分隔</span></h2>
          <div className="row kw-add">
            <textarea className="input" rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder="ai character chat" />
            <select value={addKind} onChange={(e) => setAddKind(e.target.value as KeywordKind)}>
              <option value="target">目标词</option>
              <option value="scene">场景词</option>
            </select>
            <button className="btn" disabled={!text.trim() || !!busy} onClick={add}>添加</button>
          </div>
        </section>
        {note && <p className="src-note kw-msg">{note}</p>}
        {err && <p className="error kw-msg">{err}</p>}
        <section className="panel">
          <h2>关键词 <span className="mono">{rows.length}</span></h2>
          {rows.length === 0 ? (
            <p className="src-note">
              {list?.to ? "还没有关键词。在上面添加目标词或场景词；Search Console 里展示量靠前的查询会自动列为「意外流量」。" : "绑定 Search Console 并同步后，展示量靠前的查询会自动出现在这里；也可以先手动添加目标词。"}
            </p>
          ) : (
            <table className="qtable kwtable">
              <thead>
                <tr>
                  <th><input type="checkbox" checked={allOn} onChange={toggleAll} aria-label="全选" /></th>
                  <th>关键词</th><th>类型</th>
                  <th className="num">GSC 排名</th><th className="num">展示</th>
                  <th className="num">搜索量</th><th className="num">KD</th><th className="num">CPC</th>
                  <th>备注</th><th />
                </tr>
              </thead>
              <tbody>
                {rows.map((k) => (
                  <tr key={k.id}>
                    <td><input type="checkbox" checked={selected.has(k.id)} onChange={() => toggle(k.id)} aria-label={`选择 ${k.keyword}`} /></td>
                    <td>{k.keyword}</td>
                    <td>
                      <select className={`tag tag-${k.kind}`} value={k.kind} disabled={!!busy} onChange={(e) => setKindOf(k, e.target.value as KeywordKind)}>
                        {KINDS.map((v) => <option key={v} value={v}>{kindLabel[v]}</option>)}
                      </select>
                    </td>
                    <td className="num">{k.position != null ? k.position.toFixed(1) : "—"}</td>
                    <td className="num">{k.impressions ? fmtInt(k.impressions) : "—"}</td>
                    <td className="num" title={metricNote(k.volume)}>
                      {k.volume.value != null ? fmtInt(k.volume.value) : "—"}
                      {k.volume.source === "bing" && <span className="chg new">Bing</span>}
                    </td>
                    <td className={`num ${kdClass(k.kd.value)}`} title={metricNote(k.kd)}>{k.kd.value != null ? Math.round(k.kd.value) : "—"}</td>
                    <td className="num" title={metricNote(k.cpc)}>{k.cpc.value != null ? `$${k.cpc.value.toFixed(2)}` : "—"}</td>
                    <td><input className="input kw-notes" defaultValue={k.notes} onBlur={(e) => saveNotes(k, e.target.value)} /></td>
                    <td><button className="btn" disabled={!!busy} onClick={() => remove(k)}>删除</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <p className="src-note">
            排名和展示来自 Search Console（最近 28 天，平均排名按展示加权），保存在本机。搜索量标「Bing」的是 Bing 近 4 周的精确匹配搜索次数（免费）；
            其余搜索量、KD（哥飞版）和 CPC 来自 SiteMoment 数据（扣积分，结果缓存 30 天）。鼠标停在数字上可以看来源和日期。市场：美国 / 英语。
          </p>
        </section>
      </div>
      {quote && (
        <div className="modal" onClick={() => setQuote(null)}>
          <div className="modal-box" onClick={(e) => e.stopPropagation()}>
            <h3>用 SiteMoment 数据取数</h3>
            <p>{quote.label}：{quote.keywords} 个词的搜索量、KD、CPC（美国市场）。只发送关键词，不发送你的 Search Console 数据。</p>
            <p>{quote.cached ? "这些词都有缓存，这次不扣积分。" : `最多扣 ${fmtCredits(quote.maxCredits)} 积分，按实际扣费；缓存命中的部分不扣。`}</p>
            {account?.signedIn && account.creditsLimit > 0 && (
              <p className="mono">本月已用 {fmtCredits(account.creditsUsed)} / {fmtCredits(account.creditsLimit)}</p>
            )}
            <div className="row">
              <span className="grow" />
              <button className="btn" onClick={() => setQuote(null)}>取消</button>
              <button className="btn primary" onClick={fetchCloud}>确认取数</button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
