import { Browser, Events, type CancellablePromise } from "@wailsio/runtime";
import {
  AccountService, AgentService, BingService, CollectorService, GoogleService, HealthService, InfoService, KeywordService, ModelService, NoticeService,
  OverviewService, ReportService, SiteService, SyncService, TaskService,
} from "../bindings/github.com/sitemoment/sitemoment-desktop/internal/app";
import type {
  Account, BingSite, CloudQuote, CollectorStats, CollectorView, Connection, FetchResult, GAProperty, HealthView, Info, KeywordKind, KeywordList,
  ModelSettings, NoticeList, Overview, PageFilter, PageList, Property, Report, RunView, SessionView, Site, SiteHealth, SpendMode, SyncStatus,
  TaskView, Transcript,
} from "./types";

// The generated bindings (typed via `wails3 generate bindings -ts -i`) return
// CancellablePromise of generated model types that are structurally the same
// JSON shapes as types.ts; the app works with the plain types.ts names.
const call = <T,>(p: PromiseLike<unknown>) => Promise.resolve(p) as Promise<T>;

/**
 * Login and Connect can block for up to 5 minutes if the user abandons the
 * browser. Callers need the raw cancellable promise so the UI can offer a
 * "cancel" action instead of waiting it out. This mirrors the real
 * CancellablePromise.cancel signature from @wailsio/runtime.
 */
export interface Cancellable<T> extends PromiseLike<T> {
  cancel(cause?: unknown): CancellablePromise<void>;
}

/** The account shown when nobody is signed in. */
export const signedOut: Account = {
  signedIn: false, email: "", name: "", avatarUrl: "", plan: "", creditsMonth: "", creditsUsed: 0, creditsLimit: 0,
};

export const api = {
  account: () => call<Account>(AccountService.Get()),
  login: () => AccountService.Login() as Cancellable<Account>,
  logout: () => call<void>(AccountService.Logout()),
  googleConfigured: () => call<boolean>(GoogleService.Configured()),
  connectGoogle: () => GoogleService.Connect() as Cancellable<Connection>,
  connections: () => call<Connection[]>(GoogleService.Connections()),
  disconnectGoogle: (account: string) => call<void>(GoogleService.Disconnect(account)),
  properties: (account: string) => call<Property[]>(GoogleService.Properties(account)),
  gaProperties: (account: string) => call<GAProperty[]>(GoogleService.GAProperties(account)),
  bingKeys: () => call<Connection[]>(BingService.Keys()),
  addBingKey: (key: string) => call<Connection>(BingService.AddKey(key)),
  removeBingKey: (account: string) => call<void>(BingService.RemoveKey(account)),
  bingSites: (account: string) => call<BingSite[]>(BingService.Sites(account)),
  sites: () => call<Site[]>(SiteService.List()),
  addSite: (host: string) => call<Site>(SiteService.Add(host)),
  removeSite: (id: number) => call<void>(SiteService.Remove(id)),
  bindGSC: (id: number, account: string, property: string) => call<void>(SiteService.BindGSC(id, account, property)),
  bindGA: (id: number, account: string, property: string) => call<void>(SiteService.BindGA(id, account, property)),
  bindBing: (id: number, account: string, siteUrl: string) => call<void>(SiteService.BindBing(id, account, siteUrl)),
  syncNow: (id: number) => call<void>(SyncService.SyncNow(id)),
  syncStatuses: () => call<SyncStatus[]>(SyncService.Statuses()),
  overview: (id: number, days: number) => call<Overview>(OverviewService.Get(id, days)),
  keywords: (siteId: number, kind: "" | KeywordKind) => call<KeywordList>(KeywordService.List(siteId, kind)),
  addKeywords: (siteId: number, text: string, kind: KeywordKind) => call<number>(KeywordService.Add(siteId, text, kind)),
  updateKeyword: (id: number, kind: KeywordKind, notes: string) => call<void>(KeywordService.Update(id, kind, notes)),
  removeKeyword: (id: number) => call<void>(KeywordService.Remove(id)),
  fetchBing: (siteId: number, ids: number[]) => call<FetchResult>(KeywordService.FetchBing(siteId, ids)),
  quoteCloud: (siteId: number, ids: number[]) => call<CloudQuote>(KeywordService.QuoteCloud(siteId, ids)),
  fetchCloud: (siteId: number, ids: number[]) => call<FetchResult>(KeywordService.FetchCloud(siteId, ids)),
  info: () => call<Info>(InfoService.Get()),
  sessions: (siteId: number) => call<SessionView[]>(AgentService.Sessions(siteId)),
  createSession: (siteId: number) => call<SessionView>(AgentService.Create(siteId)),
  deleteSession: (id: number) => call<void>(AgentService.Delete(id)),
  transcript: (id: number) => call<Transcript>(AgentService.Transcript(id)),
  send: (id: number, text: string) => call<void>(AgentService.Send(id, text)),
  stop: (id: number) => call<void>(AgentService.Stop(id)),
  confirm: (id: number, approve: boolean) => call<void>(AgentService.Confirm(id, approve)),
  setMode: (id: number, mode: SpendMode) => call<SessionView>(AgentService.SetMode(id, mode)),
  modelLabel: () => call<string>(AgentService.ModelLabel()),
  modelSettings: () => call<ModelSettings>(ModelService.Settings()),
  saveModelSettings: (m: ModelSettings) => call<ModelSettings>(ModelService.Save(m)),
  setModelKey: (provider: "openai" | "anthropic", key: string) => call<ModelSettings>(ModelService.SetKey(provider, key)),
  hostedModels: () => call<string[]>(ModelService.HostedModels()),
  testModel: () => call<string>(ModelService.Test()),
  collectors: () => call<CollectorView[]>(CollectorService.List()),
  rescanCollectors: () => call<CollectorView[]>(CollectorService.Rescan()),
  // version, hosts and sha must be exactly what the Collectors page showed
  // for id (Catalog.Approve rejects anything else).
  approveCollector: (id: string, version: string, hosts: string[], sha: string) =>
    call<CollectorView[]>(CollectorService.Approve(id, version, hosts, sha)),
  collectorDir: () => call<string>(CollectorService.Dir()),
  collectorRuns: (limit: number) => call<RunView[]>(CollectorService.Runs(limit)),
  needsHuman: () => call<RunView[]>(CollectorService.NeedsHuman()),
  revealRun: (runId: number) => call<void>(CollectorService.Reveal(runId)),
  collectorStats: () => call<CollectorStats>(CollectorService.Stats()),
  health: (siteId: number) => call<HealthView>(HealthService.Get(siteId)),
  runHealth: (siteId: number) => call<void>(HealthService.Run(siteId)),
  healthScores: () => call<SiteHealth[] | null>(HealthService.Scores()),
  pages: (siteId: number, filter: PageFilter, contains: string) => call<PageList>(HealthService.Pages(siteId, filter, contains)),
  tasks: (siteId: number) => call<TaskView[] | null>(TaskService.List(siteId)),
  confirmTask: (id: number) => call<TaskView>(TaskService.Confirm(id)),
  dismissTask: (id: number) => call<void>(TaskService.Dismiss(id)),
  deleteTask: (id: number) => call<void>(TaskService.Delete(id)),
  checkTask: (id: number) => call<TaskView>(TaskService.Check(id)),
  autoAcceptTasks: () => call<boolean>(TaskService.AutoAccept()),
  setAutoAcceptTasks: (on: boolean) => call<void>(TaskService.SetAutoAccept(on)),
  reports: (siteId: number) => call<Report[] | null>(ReportService.List(siteId)),
  report: (id: number) => call<Report>(ReportService.Get(id)),
  generateReport: (siteId: number) => call<Report>(ReportService.Generate(siteId)),
  exportReport: (id: number, format: "html" | "md") => call<string>(ReportService.Export(id, format)),
  deleteReport: (id: number) => call<void>(ReportService.Delete(id)),
  notices: () => call<NoticeList>(NoticeService.List()),
  markNoticeRead: (id: number) => call<void>(NoticeService.MarkRead(id)),
};

/** Opens an http(s) link in the system browser; anything else is ignored. */
export function openURL(href: string) {
  if (/^https?:\/\//i.test(href)) Browser.OpenURL(href).catch(() => {});
}

/** Subscribes to a Go event (application Event.Emit); returns the unsubscribe function. */
export function onEvent<T>(name: string, fn: (data: T) => void): () => void {
  return Events.On(name, (ev) => fn(ev.data as T));
}

/** Matches Go net package errors such as "dial tcp ...", "no such host", and client timeouts. */
export const isNetworkError = (msg: string) => /dial tcp|no such host|timeout/.test(msg);

/** Translates known Go error messages into Chinese for the UI; anything else passes through unchanged. */
export const errText = (e: unknown) => {
  const msg = e instanceof Error ? e.message : String(e);
  if (msg.includes("access_denied")) return "已取消登录";
  if (msg.includes("state mismatch")) return "登录请求不匹配，请重试";
  if (msg.includes("Search Console access was not granted")) return "没有授予 Search Console 读取权限，请重新连接并勾选";
  if (msg.includes("Analytics access was not granted")) return "没有授予 Google Analytics 读取权限，请重新连接 Google 账号并勾选";
  if (msg.includes("authorization expired, connect the account again")) return "Google 授权已失效，请重新连接";
  if (msg.includes("bing: the API key is not valid")) return "Bing API key 无效，请检查后重新添加";
  if (msg.includes("bing: Bing Webmaster is throttling")) return "Bing 暂时限流，请稍后再试";
  if (msg.includes("bing: this key cannot read that site")) return "这个 Bing key 读不到该站点";
  if (msg.includes("cloud: not signed in")) return "请先登录 SiteMoment";
  if (msg.includes("store: a keyword is 1 to 200 characters")) return "关键词不能为空，最长 200 个字符";
  if (msg.includes("llm:") && msg.includes("credits_exhausted")) return "本月 SiteMoment 额度已用完，可以在设置 → AI 模型里改用自己的 key";
  if (msg.includes("credits_exhausted")) return "本月额度已用完";
  if (msg.includes("request_too_large")) return "这次请求太大，请减少关键词数量";
  if (msg.includes("data_budget")) return "数据服务今天的总额度已用完，请明天再试";
  if (msg.includes("rate_limited")) return "今天的数据请求次数已用完，请明天再试";
  if (msg.includes("too_many_requests")) return "请等上一个取数完成";
  // The model's own messages are already Chinese; just drop the "llm: " prefix.
  if (msg.includes("模型回复太长，已中止")) return "模型回复太长，已中止";
  if (msg.includes("模型回复中断")) return "模型回复中断";
  if (isNetworkError(msg)) return "网络连接失败，请检查网络";
  return msg;
};
