import type { Account, Info, Site } from "../types";
export function SettingsPage(_: { account: Account | null; info: Info | null; sites: Site[]; onAccount: (a: Account) => void; onLogin: () => void; onSitesChanged: () => void }) {
  return <div className="content"><div className="empty">设置</div></div>;
}
