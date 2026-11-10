import { fmtBytes, fmtTime } from "../format";
import type { Info, SyncProgress, SyncStatus } from "../types";

export function StatusBar({ status, progress, info }: { status?: SyncStatus; progress: SyncProgress | null; info: Info | null }) {
  let left = "未同步";
  if (status?.state === "running") {
    left = progress ? `同步中 ${progress.slice} ${progress.done}/${progress.total}` : "同步中";
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
