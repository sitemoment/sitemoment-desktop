import type { ReactNode } from "react";
import { fmtBytes, fmtCredits, syncStatusLabel } from "../format";
import type { Account, CollectorStats, Info, SyncProgress, SyncStatus } from "../types";

export function StatusBar({ status, progress, info, siteId, account, collect, paused, onStop, children }: {
  status?: SyncStatus; progress: SyncProgress | null; info: Info | null; siteId?: number; account?: Account | null;
  collect?: CollectorStats | null; paused?: boolean; onStop?: () => void; children?: ReactNode;
}) {
  const ownProgress = progress && progress.siteId === siteId ? progress : null;
  const left = syncStatusLabel(status, Boolean(paused), ownProgress);
  return (
    <div className="statusbar">
      <span>{left}</span>
      {!paused && status?.state === "running" && onStop && <button className="btn" onClick={onStop}>停止同步</button>}
      {collect && (collect.running > 0 || collect.queued > 0) && (
        <span>{collect.running > 0 && <i className="spin" />} 采集器运行 {collect.running}，排队 {collect.queued}</span>
      )}
      {collect?.today.filter((d) => d.count > 0).map((d) => <span key={d.collector}>{d.name} {d.count} / {d.limit}</span>)}
      <span className="right">
        {children}
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
