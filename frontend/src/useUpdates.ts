import { useCallback, useEffect, useRef, useState } from "react";
import { api, errText, onEvent } from "./api";
import type { UpdateStatus } from "./types";
import { type UpdateAction, updateErrorText } from "./updates";

/** The update status (kept current by "update:status") and its actions. */
export function useUpdates() {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [err, setErr] = useState("");
  // A double-click (or a second click before "update:status" repaints the
  // button) must not fire the action twice: Install racing itself hits the
  // updater's ErrDownloadInProgress, which is fine to ignore quietly, but
  // there is no reason to let it happen at all.
  const pending = useRef(false);
  useEffect(() => {
    api.updateStatus().then(setStatus).catch(() => {});
    return onEvent<UpdateStatus>("update:status", setStatus);
  }, []);
  const act = useCallback(async (a: UpdateAction) => {
    if (pending.current) return;
    pending.current = true;
    setErr("");
    try {
      if (a === "check") setStatus(await api.checkUpdate());
      else if (a === "install") await api.installUpdate();
      else await api.restartToUpdate();
    } catch (e) {
      const msg = errText(e);
      if (!/download already in progress/i.test(msg)) setErr(updateErrorText(msg));
    } finally {
      pending.current = false;
    }
  }, []);
  const setAuto = useCallback(async (on: boolean) => {
    try {
      setStatus(await api.setAutoUpdate(on));
    } catch (e) {
      setErr(errText(e));
    }
  }, []);
  return { status, err, act, setAuto };
}
