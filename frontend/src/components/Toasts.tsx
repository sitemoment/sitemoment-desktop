import { useEffect, useState } from "react";
import { onEvent } from "../api";
import type { Note } from "../types";

type Toast = Note & { id: number };

/** In-app notifications ("notify" events: a new version, the notification test). */
export function Toasts() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  useEffect(() => {
    let next = 0;
    return onEvent<Note>("notify", (n) => {
      const id = ++next;
      setToasts((t) => [...t.slice(-2), { ...n, id }]);
      setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 8000);
    });
  }, []);
  if (!toasts.length) return null;
  return (
    <div className="toasts" role="status">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`}>
          <strong>{t.title}</strong>
          <span>{t.body}</span>
        </div>
      ))}
    </div>
  );
}
