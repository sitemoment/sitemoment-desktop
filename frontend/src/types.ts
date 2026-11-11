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

// Agent (internal/agent, app.AgentService).
export type SpendMode = "free_only" | "confirm" | "auto";
export type SessionStatus = "idle" | "running" | "awaiting_confirm";
export interface SessionView {
  id: number; siteId: number; title: string; spendMode: SpendMode; status: SessionStatus; credits: number; updatedAt: number;
}
export interface CardLine { s: "ok" | "warn" | "fail"; t: string }
export interface Card {
  type: "table" | "lines"; title?: string; columns?: unknown[]; rows?: unknown[][]; items?: CardLine[]; note?: string;
}
export type ToolKind = "local" | "web" | "collector" | "cloud";
export interface ToolView {
  callId: string; tool: string; label: string; kind: ToolKind; status: "running" | "ok" | "error" | "declined";
  cached: boolean; credits: number; card?: Card | null; error?: string; ms: number;
}
export interface Item {
  kind: "user" | "answer" | "note" | "tool" | "error" | "stopped"; turn: number; text?: string; tool?: ToolView | null; at: number;
}
export interface PendingCall { callId: string; tool: string; label: string; maxCredits: number }
export interface Pending { calls: PendingCall[]; totalMax: number }
export interface Transcript { session: SessionView; items: Item[]; pending: Pending | null }
/** Payload of the "agent:event" event. */
export type AgentEvent = { sessionId: number; turn: number } & (
  | { type: "turn_started" }
  | { type: "text"; delta: string }
  | { type: "note"; content: string }
  | { type: "tool_started"; tool: ToolView }
  | { type: "tool_finished"; tool: ToolView }
  | { type: "confirm_needed"; pending: Pending }
  | { type: "message"; content: string }
  | { type: "error"; message: string }
  | { type: "stopped"; content: string }
  | { type: "turn_finished"; status: SessionStatus; credits: number }
);

// AI model settings (app.ModelService).
export type Provider = "hosted" | "openai" | "anthropic";
export interface ModelSettings {
  provider: Provider; hostedModel: string; openaiBaseUrl: string; openaiModel: string; anthropicModel: string;
  openaiKeySet: boolean; anthropicKeySet: boolean; label: string;
}

// Collectors (internal/collect, app.CollectorService).
export interface CollectorView {
  id: string; name: string; tool: string; version: string; hosts: string[];
  // sha is the collector package's sha256 (Collector.SHA); Approve must be
  // called with exactly this version, hosts and sha, since that is what
  // the Collectors page showed the user before they approved it.
  sha: string; builtin: boolean;
  status: "enabled" | "needs_approval" | "invalid"; error: string; dailyLimit: number; cacheHours: number; minGapSeconds: number;
}
export interface RunView {
  id: number; collector: string; input: string; status: "running" | "need_human" | "ok" | "error" | "cancelled";
  error: string; createdAt: number; finishedAt: number;
}
export interface DayCount { collector: string; name: string; count: number; limit: number }
export interface CollectorStats { running: number; queued: number; today: DayCount[] }
