import { fmtBytes, fmtCredits, fmtTime } from "../format";
import type { Account, Info, SyncProgress, SyncStatus } from "../types";

export function StatusBar({ status, progress, info, siteId, account }: {
  status?: SyncStatus; progress: SyncProgress | null; info: Info | null; siteId?: number; account?: Account | null;
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
        {account?.signedIn && account.creditsLimit > 0 && (
          <span title={`${account.creditsMonth} 的 SiteMoment 积分（托管模型和付费数据共用）`}>
            额度 {fmtCredits(account.creditsUsed)} / {fmtCredits(account.creditsLimit)}
          </span>
        )}
        {info && <span>v{info.version}</span>}
      </span>
    </div>
  );
}
