import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { api, errText, onEvent } from "../api";
import type { Account, AgentEvent, Item, SessionView, Site, SpendMode } from "../types";
import { ConfirmBar } from "./ConfirmBar";
import { MarkdownView } from "./markdown";
import { agentReducer, emptyAgent } from "./reducer";
import { ToolCard } from "./ToolCard";

const modeLabel: Record<SpendMode, string> = { free_only: "只用免费数据", confirm: "付费数据：先确认", auto: "付费数据：自动" };
const CONFIRM_MS = 3000; // two-click delete, see Settings.tsx for why not window.confirm()

/** Groups consecutive tool items so they render in one box, as in the mockup. */
function groups(items: Item[]): (Item | Item[])[] {
  const out: (Item | Item[])[] = [];
  for (const it of items) {
    const last = out[out.length - 1];
    if (it.kind === "tool" && Array.isArray(last)) last.push(it);
    else out.push(it.kind === "tool" ? [it] : it);
  }
  return out;
}

function ItemView({ item }: { item: Item }) {
  switch (item.kind) {
    case "user": return <div className="msg msg-user">{item.text}</div>;
    case "note": return <div className="msg msg-note"><MarkdownView text={item.text ?? ""} /></div>;
    case "answer": return (
      <div className="msg msg-ai">
        <MarkdownView text={item.text ?? ""} />
        <div className="msg-actions"><button onClick={() => navigator.clipboard?.writeText(item.text ?? "")}>复制</button></div>
      </div>
    );
    case "error": return <div className="msg msg-err">{item.text}</div>;
    case "stopped": return <div className="msg msg-stop">{item.text}</div>;
    default: return null;
  }
}

/** The right-hand SEO Agent panel for the selected site. */
export function AgentPanel({ site, account, modelRev, onOpenSettings, onCreditsUsed }: {
  site: Site | null; account: Account | null; modelRev: number; onOpenSettings: () => void; onCreditsUsed: () => void;
}) {
  const [sessions, setSessions] = useState<SessionView[]>([]);
  const [sessionId, setSessionId] = useState<number | null>(null);
  const [state, dispatch] = useReducer(agentReducer, emptyAgent);
  const [text, setText] = useState("");
  const [model, setModel] = useState("");
  const [modelErr, setModelErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const logRef = useRef<HTMLDivElement>(null);
  const deleteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const siteId = site?.id ?? null;

  const loadModel = useCallback(() => {
    api.modelLabel().then((l) => { setModel(l); setModelErr(""); }, (e) => { setModel(""); setModelErr(errText(e)); });
  }, []);
  const loadSessions = useCallback(async (keep?: number | null) => {
    if (siteId === null) return;
    const list = await api.sessions(siteId);
    setSessions(list);
    setSessionId((cur) => {
      const want = keep !== undefined ? keep : cur;
      return want !== null && list.some((s) => s.id === want) ? want : list[0]?.id ?? null;
    });
  }, [siteId]);
  const loadTranscript = useCallback(async (id: number) => {
    dispatch({ type: "load", transcript: await api.transcript(id) });
  }, []);

  // Reload the model label when the settings or the sign-in change.
  useEffect(loadModel, [loadModel, modelRev, account?.signedIn]);
  useEffect(() => {
    setSessions([]);
    setSessionId(null);
    loadSessions(null).catch((e) => setErr(errText(e)));
  }, [loadSessions]);
  useEffect(() => {
    if (sessionId === null) {
      dispatch({ type: "clear" });
      return;
    }
    loadTranscript(sessionId).catch((e) => setErr(errText(e)));
  }, [sessionId, loadTranscript]);
  useEffect(() => onEvent<AgentEvent>("agent:event", (ev) => {
    dispatch({ type: "event", event: ev });
    if (ev.type === "turn_finished") {
      // The stored transcript is the source of truth once a turn ends.
      loadTranscript(ev.sessionId).catch(() => {});
      loadSessions().catch(() => {});
      if (ev.credits > 0) onCreditsUsed();
    }
  }), [loadTranscript, loadSessions, onCreditsUsed]);
  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [state.items, state.streaming, state.pending]);
  useEffect(() => () => { if (deleteTimer.current) clearTimeout(deleteTimer.current); }, []);

  const session = state.session && state.session.id === sessionId ? state.session : null;
  const running = session?.status === "running";

  const newSession = async () => {
    if (siteId === null) return;
    setErr("");
    try {
      const s = await api.createSession(siteId);
      await loadSessions(s.id);
    } catch (e) { setErr(errText(e)); }
  };
  const send = async () => {
    const q = text.trim();
    if (!q || siteId === null || running) return;
    setErr("");
    setBusy(true);
    try {
      let id = sessionId;
      if (id === null) {
        const s = await api.createSession(siteId);
        await loadSessions(s.id);
        await loadTranscript(s.id);
        id = s.id;
      }
      dispatch({ type: "sent", text: q, at: Date.now() / 1000 });
      setText("");
      await api.send(id, q);
    } catch (e) {
      setErr(errText(e));
      if (sessionId !== null) loadTranscript(sessionId).catch(() => {});
    } finally { setBusy(false); }
  };
  const stop = () => { if (sessionId !== null) api.stop(sessionId).catch((e) => setErr(errText(e))); };
  const answer = async (approve: boolean) => {
    if (sessionId === null) return;
    setBusy(true);
    dispatch({ type: "confirmed" });
    try { await api.confirm(sessionId, approve); } catch (e) { setErr(errText(e)); loadTranscript(sessionId).catch(() => {}); } finally { setBusy(false); }
  };
  const setMode = async (mode: SpendMode) => {
    if (sessionId === null) return;
    try {
      await api.setMode(sessionId, mode);
      await loadTranscript(sessionId);
      await loadSessions();
    } catch (e) { setErr(errText(e)); }
  };
  const remove = async () => {
    if (sessionId === null) return;
    if (!confirmDelete) {
      setConfirmDelete(true);
      deleteTimer.current = setTimeout(() => setConfirmDelete(false), CONFIRM_MS);
      return;
    }
    if (deleteTimer.current) clearTimeout(deleteTimer.current);
    setConfirmDelete(false);
    try { await api.deleteSession(sessionId); await loadSessions(null); } catch (e) { setErr(errText(e)); }
  };

  return (
    <aside className="agent">
      <div className="agent-head">
        <b>SEO Agent</b>
        <button className="model" title={modelErr || "更换模型：设置 → AI 模型"} onClick={onOpenSettings}>
          {model || <span className="st-warn">未配置模型</span>}
        </button>
      </div>
      {site && (
        <div className="agent-sessions">
          <select value={sessionId ?? ""} onChange={(e) => setSessionId(e.target.value ? Number(e.target.value) : null)}>
            {sessions.length === 0 && <option value="">新会话</option>}
            {sessions.map((s) => <option key={s.id} value={s.id}>{s.title || `会话 #${s.id}`}</option>)}
          </select>
          <button className="btn" onClick={newSession} title="新会话">＋</button>
          {sessionId !== null && <button className="btn" disabled={running} onClick={remove}>{confirmDelete ? "确认删除" : "删除"}</button>}
        </div>
      )}
      <div className="agent-log" ref={logRef}>
        {!site && <p className="agent-empty">先在左侧选择或添加一个站点。</p>}
        {site && state.items.length === 0 && !state.streaming && (
          <p className="agent-empty">问 Agent 关于 {site.host} 的问题，比如「这个词卡在第二页，下一步怎么做？」。它会先查本机的 GSC / GA4 / Bing 数据和 Google 搜索结果，再给出结论、依据、建议和验收。</p>
        )}
        {groups(state.items).map((g, i) => Array.isArray(g) ? (
          <div className="tools" key={i}>{g.map((it) => <ToolCard key={it.tool!.callId + it.turn} tool={it.tool!} />)}</div>
        ) : <ItemView key={i} item={g} />)}
        {state.streaming && <div className="msg msg-ai streaming"><MarkdownView text={state.streaming} /></div>}
        {running && !state.streaming && <div className="agent-wait"><i className="spin" /> 思考中…</div>}
      </div>
      {state.pending && session?.status === "awaiting_confirm" && (
        <ConfirmBar pending={state.pending} account={account} busy={busy} onAnswer={answer} />
      )}
      {err && <p className="error agent-err">{err}</p>}
      <div className="composer">
        <div className="composer-box">
          <textarea rows={2} value={text} disabled={!site} placeholder="问 Agent：比如「这周哪些页面掉得最厉害？」"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); }
            }} />
          <div className="composer-bar">
            {site && <span className="chip">站点：{site.host}</span>}
            <select className="chip" value={session?.spendMode ?? "confirm"} disabled={!session || running} onChange={(e) => setMode(e.target.value as SpendMode)}>
              {(Object.keys(modeLabel) as SpendMode[]).map((m) => <option key={m} value={m}>{modeLabel[m]}</option>)}
            </select>
            <span className="grow" />
            {running ? (
              <button className="send stop" aria-label="停止" title="停止" onClick={stop}>■</button>
            ) : (
              <button className="send" aria-label="发送" title="发送（Enter）" disabled={busy || !text.trim() || !site} onClick={send}>↑</button>
            )}
          </div>
        </div>
      </div>
    </aside>
  );
}
