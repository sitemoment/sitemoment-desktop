// JSON shapes returned by the Go services (internal/app, internal/store, internal/google).
export interface Account { signedIn: boolean; email: string; name: string; avatarUrl: string; plan: string }
export interface Site { id: number; host: string; name: string; googleAccount: string; gscProperty: string; createdAt: number }
export interface Connection { kind: string; account: string; createdAt: number }
export interface Property { siteUrl: string; permissionLevel: string }
export interface Totals { clicks: number; impressions: number; ctr: number; position: number }
export interface DayPoint { day: string; clicks: number; impressions: number }
export interface QueryStat { query: string; clicks: number; impressions: number; position: number; prevPosition: number | null }
export interface Overview {
  site: Site; synced: boolean; days: number; from: string; to: string; prevFrom: string; prevTo: string;
  current: Totals; previous: Totals; daily: DayPoint[]; prevDaily: DayPoint[]; queries: QueryStat[];
}
export interface SyncStatus { siteId: number; state: "running" | "done" | "failed"; error?: string; requests: number; at: number }
export interface SyncProgress { siteId: number; slice: string; day: string; done: number; total: number }
export interface Info { version: string; dataDir: string; dbBytes: number }
