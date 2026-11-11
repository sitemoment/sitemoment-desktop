// JSON shapes returned by the Go services (internal/app, internal/store, internal/google, internal/bing).
export interface Account {
  signedIn: boolean; email: string; name: string; avatarUrl: string; plan: string;
  creditsMonth: string; creditsUsed: number; creditsLimit: number;
}
export interface Site {
  id: number; host: string; name: string; googleAccount: string; gscProperty: string;
  gaAccount: string; gaProperty: string; bingAccount: string; bingSite: string; createdAt: number;
}
export interface Connection { kind: string; account: string; createdAt: number }
export interface Property { siteUrl: string; permissionLevel: string }
export interface GAProperty { property: string; displayName: string; account: string; accountName: string }
export interface BingSite { url: string; verified: boolean }
export interface Totals { clicks: number; impressions: number; ctr: number; position: number }
export interface DayPoint { day: string; clicks: number; impressions: number }
export interface QueryStat { query: string; clicks: number; impressions: number; position: number; prevPosition: number | null }
export interface Tile { source: "ga" | "bing"; label: string; from: string; to: string; current: number; previous: number; daily: number[] }
export interface Overview {
  site: Site; synced: boolean; days: number; from: string; to: string; prevFrom: string; prevTo: string;
  current: Totals; previous: Totals; daily: DayPoint[]; prevDaily: DayPoint[]; queries: QueryStat[]; tiles: Tile[];
}
export interface SyncStatus { siteId: number; state: "running" | "done" | "failed"; error?: string; requests: number; at: number }
export interface SyncProgress { siteId: number; slice: string; day: string; done: number; total: number }
export interface Info { version: string; dataDir: string; dbBytes: number }
export type KeywordKind = "target" | "found" | "scene";
export interface Metric { value: number | null; source: "" | "bing" | "sitemoment"; at: number }
export interface Keyword {
  id: number; siteId: number; keyword: string; kind: KeywordKind; volume: Metric; kd: Metric; cpc: Metric;
  notes: string; createdAt: number; clicks: number; impressions: number; position: number | null;
}
export interface KeywordList { from: string; to: string; keywords: Keyword[] }
export interface FetchResult { updated: number; skipped: number; credits: number; cached: boolean; source: string }
export interface CloudQuote { tool: string; label: string; keywords: number; maxCredits: number; cached: boolean }
