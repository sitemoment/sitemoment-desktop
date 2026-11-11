import { fmtCredits } from "../format";
import type { Account, Pending } from "../types";

/** Asks before the agent spends SiteMoment credits on paid data. */
export function ConfirmBar({ pending, account, busy, onAnswer }: {
  pending: Pending; account: Account | null; busy: boolean; onAnswer: (approve: boolean) => void;
}) {
  const left = account?.signedIn && account.creditsLimit > 0 ? account.creditsLimit - account.creditsUsed : null;
  return (
    <div className="confirm-bar">
      <b>Agent 想调用付费数据，最多扣 {fmtCredits(pending.totalMax)} 积分</b>
      <ul>
        {pending.calls.map((c) => <li key={c.callId}><span>{c.label}</span><span className="num">≤ {fmtCredits(c.maxCredits)}</span></li>)}
      </ul>
      {left !== null && <p className="src-note">本月还剩 {fmtCredits(left)} 积分（托管模型和付费数据共用），缓存命中不扣。</p>}
      <div className="row">
        <button className="btn primary" disabled={busy} onClick={() => onAnswer(true)}>允许并继续</button>
        <button className="btn" disabled={busy} onClick={() => onAnswer(false)}>不用付费数据</button>
      </div>
    </div>
  );
}
