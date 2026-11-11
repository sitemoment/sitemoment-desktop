import { useCallback, useEffect, useState } from "react";
import { api, onEvent } from "../api";
import { fmtDateTime } from "../format";
import type { Notice, NoticeList } from "../types";

/** The status bar's notices button and list (in-app only; OS notifications come with M5). */
export function Notices({ onOpen }: { onOpen: (n: Notice) => void }) {
  const [list, setList] = useState<NoticeList>({ items: [], unread: 0 });
  const [open, setOpen] = useState(false);
  const load = useCallback(() => { api.notices().then(setList).catch(() => {}); }, []);
  useEffect(() => {
    load();
    return onEvent<Notice>("notice:new", load);
  }, [load]);
  const pick = (n: Notice) => {
    setOpen(false);
    if (!n.read) api.markNoticeRead(n.id).then(load);
    onOpen(n);
  };
  return (
    <span className="notices">
      <button className={"notice-btn" + (list.unread ? " on" : "")} onClick={() => setOpen(!open)}>通知 {list.unread || ""}</button>
      {open && (
        <div className="notice-pop">
          <div className="row notice-top">
            <b className="grow">通知</b>
            <button className="link" disabled={!list.unread} onClick={() => api.markNoticeRead(0).then(load)}>全部已读</button>
          </div>
          {list.items.length === 0 && <p className="src-note">没有通知。</p>}
          {list.items.map((n) => (
            <button key={n.id} className={"notice" + (n.read ? "" : " unread")} onClick={() => pick(n)}>
              <b>{n.title}</b>
              {n.body && <small>{n.body}</small>}
              <small className="mono">{fmtDateTime(n.createdAt)}</small>
            </button>
          ))}
        </div>
      )}
    </span>
  );
}
