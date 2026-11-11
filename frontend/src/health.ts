import { useCallback, useEffect, useRef, useState } from "react";
import { api, errText, onEvent } from "./api";
import type { HealthStatus, HealthView } from "./types";

/** Loads a site's health view and reloads it when a check of the site starts or ends. */
export function useHealth(siteId: number) {
  const [view, setView] = useState<HealthView | null>(null);
  const [err, setErr] = useState("");
  // A ref, not state: it must block a second click before React has had a
  // chance to re-render with a state-based flag.
  const busy = useRef(false);
  const load = useCallback(() => {
    api.health(siteId).then((v) => { setView(v); setErr(""); }, (e) => setErr(errText(e)));
  }, [siteId]);
  useEffect(() => {
    load();
    return onEvent<HealthStatus>("health:status", (s) => { if (s.siteId === siteId) load(); });
  }, [siteId, load]);
  const run = () => {
    if (busy.current) return;
    busy.current = true;
    api.runHealth(siteId).then(load, (e) => setErr(errText(e))).finally(() => { busy.current = false; });
  };
  return { view, err, run, reload: load };
}
