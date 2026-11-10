import { Events } from "@wailsio/runtime";
import {
  AccountService, GoogleService, InfoService, OverviewService, SiteService, SyncService,
} from "../bindings/github.com/sitemoment/sitemoment-desktop/internal/app";
import type { Account, Connection, Info, Overview, Property, Site, SyncStatus } from "./types";

// The generated bindings return CancellablePromise of generated model types;
// the app works with the plain JSON shapes in types.ts.
const call = <T,>(p: PromiseLike<unknown>) => Promise.resolve(p) as Promise<T>;

/**
 * Login and Connect can block for up to 5 minutes if the user abandons the
 * browser. Callers need the raw cancellable promise so the UI can offer a
 * "cancel" action instead of waiting it out. This is the shape the generated
 * bindings' CancellablePromise satisfies, trimmed to what the UI needs.
 */
export interface Cancellable<T> extends PromiseLike<T> {
  cancel(cause?: unknown): void;
}

export const api = {
  account: () => call<Account>(AccountService.Get()),
  login: () => AccountService.Login() as unknown as Cancellable<Account>,
  logout: () => call<void>(AccountService.Logout()),
  googleConfigured: () => call<boolean>(GoogleService.Configured()),
  connectGoogle: () => GoogleService.Connect() as unknown as Cancellable<Connection>,
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

export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));
