import { Events, type CancellablePromise } from "@wailsio/runtime";
import {
  AccountService, GoogleService, InfoService, OverviewService, SiteService, SyncService,
} from "../bindings/github.com/sitemoment/sitemoment-desktop/internal/app";
import type { Account, Connection, Info, Overview, Property, Site, SyncStatus } from "./types";

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

export const api = {
  account: () => call<Account>(AccountService.Get()),
  login: () => AccountService.Login() as Cancellable<Account>,
  logout: () => call<void>(AccountService.Logout()),
  googleConfigured: () => call<boolean>(GoogleService.Configured()),
  connectGoogle: () => GoogleService.Connect() as Cancellable<Connection>,
  connections: () => call<Connection[]>(GoogleService.Connections()),
  disconnectGoogle: (account: string) => call<void>(GoogleService.Disconnect(account)),
  properties: (account: string) => call<Property[]>(GoogleService.Properties(account)),
  sites: () => call<Site[]>(SiteService.List()),
  addSite: (host: string) => call<Site>(SiteService.Add(host)),
  removeSite: (id: number) => call<void>(SiteService.Remove(id)),
  bindGSC: (id: number, account: string, property: string) => call<void>(SiteService.BindGSC(id, account, property)),
  syncNow: (id: number) => call<void>(SyncService.SyncNow(id)),
  syncStatuses: () => call<SyncStatus[]>(SyncService.Statuses()),
  overview: (id: number, days: number) => call<Overview>(OverviewService.Get(id, days)),
  info: () => call<Info>(InfoService.Get()),
};

/** Subscribes to a Go event (application Event.Emit); returns the unsubscribe function. */
export function onEvent<T>(name: string, fn: (data: T) => void): () => void {
  return Events.On(name, (ev) => fn(ev.data as T));
}

/** Matches Go net package errors such as "dial tcp ...", "no such host", and client timeouts. */
export const isNetworkError = (msg: string) => /dial tcp|no such host|timeout/.test(msg);

/** Translates known Go error messages (oauthx, google, net) into Chinese for the UI; anything else passes through unchanged. */
export const errText = (e: unknown) => {
  const msg = e instanceof Error ? e.message : String(e);
  if (msg.includes("access_denied")) return "已取消登录";
  if (msg.includes("state mismatch")) return "登录请求不匹配，请重试";
  if (msg.includes("Search Console access was not granted")) return "没有授予 Search Console 读取权限，请重新连接并勾选";
  if (msg.includes("authorization expired, connect the account again")) return "Google 授权已失效，请重新连接";
  if (isNetworkError(msg)) return "网络连接失败，请检查网络";
  return msg;
};
