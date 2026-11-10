import type { Site, SyncStatus } from "../types";
export function OverviewPage({ site }: { site: Site; status?: SyncStatus; onChanged: () => void }) {
  return <div className="content"><div className="empty">{site.host}</div></div>;
}
