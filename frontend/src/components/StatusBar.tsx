import { fmtBytes, fmtTime } from "../format";
import type { Info, SyncProgress, SyncStatus } from "../types";

export function StatusBar({ status, progress, info, siteId }: {
  status?: SyncStatus; progress: SyncProgress | null; info: Info | null; siteId?: number;
}) {
  let left = "未同步";
  const ownProgress = progress && progress.siteId === siteId ? progress : null;
  if (status?.state === "running") {
    left = ownProgress ? `同步中 ${ownProgress.slice} ${ownProgress.done}/${ownProgress.total}` : "同步中";
  } else if (status?.state === "failed") {
    left = `同步失败：${status.error ?? ""}`;
  } else if (status?.state === "done") {
    left = `● 已同步 ${fmtTime(status.at)}`;
  }
  return (
    <div className="statusbar">
      <span>{left}</span>
      <span className="right">
        {info && <span>SQLite {fmtBytes(info.dbBytes)}</span>}
        {info && <span>v{info.version}</span>}
      </span>
    </div>
  );
}
