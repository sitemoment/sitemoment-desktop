import { useCallback, useEffect, useState } from "react";
import { api, errText, onEvent } from "./api";
import type { UpdateStatus } from "./types";
import { type UpdateAction, updateErrorText } from "./updates";

/** The update status (kept current by "update:status") and its actions. */
export function useUpdates() {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    api.updateStatus().then(setStatus).catch(() => {});
    return onEvent<UpdateStatus>("update:status", setStatus);
  }, []);
  const act = useCallback(async (a: UpdateAction) => {
    setErr("");
    try {
      if (a === "check") setStatus(await api.checkUpdate());
      else if (a === "install") await api.installUpdate();
      else await api.restartToUpdate();
    } catch (e) {
      setErr(updateErrorText(errText(e)));
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
