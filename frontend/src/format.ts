import type { BingSite, GAProperty, KeywordKind, Metric, Outcome, Property, RunView, SliceProgress, SourceProgress, SyncProgress, SyncStatus, TaskStatus } from "./types";

export const fmtInt = (n: number) => Math.round(n).toLocaleString("en-US");

export function fmtCompact(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e4) {
    // Round at the same precision as the displayed value first: a number
    // like 999999 rounds to "1000.0K", which belongs in the next unit up.
    const k = Number((n / 1e3).toFixed(1));
    if (k >= 1000) return (n / 1e6).toFixed(1) + "M";
    return k.toFixed(1) + "K";
  }
  return fmtInt(n);
}

export const fmtPct = (ratio: number) => (ratio * 100).toFixed(2) + "%";

export function fmtBytes(b: number): string {
  if (b >= 1 << 30) return (b / (1 << 30)).toFixed(1) + " GB";
  if (b >= 1 << 20) return (b / (1 << 20)).toFixed(1) + " MB";
  return (b / (1 << 10)).toFixed(1) + " KB";
}

export function fmtTime(unix: number): string {
  return new Date(unix * 1000).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

/** Progress's gscsync.Progress.Slice / "ga:"+gasync's slice names, in
 * Chinese, for the status bar (which otherwise shows the raw English
 * slice key). Unknown keys pass through unchanged. */
const sliceNames: Record<string, string> = {
  date: "日期", query: "查询词", page: "页面",
  "ga:date": "日期", "ga:landing": "着陆页", "ga:channel": "渠道",
};
export const sliceLabel = (slice: string): string => sliceNames[slice] ?? slice;

/**
 * The status bar's left-hand label for a site's sync state: paused beats
 * everything else (nothing syncs while paused), then the site's own
 * SyncStatus (running — with the in-flight slice's progress if known,
 * failed, stopped, or done), else "未同步" for a site that has never synced.
 */
export function syncStatusLabel(status: SyncStatus | undefined, paused: boolean, ownProgress: SyncProgress | null): string {
  if (paused) return "已暂停";
  switch (status?.state) {
    case "running":
      return ownProgress ? `同步中 ${sliceLabel(ownProgress.slice)} ${ownProgress.done}/${ownProgress.total}` : "同步中";
    case "failed":
      return `同步失败：${status.error ?? ""}`;
    case "stopped":
      return "已停止同步";
    case "done":
      return `● 已同步 ${fmtTime(status.at)}`;
    default:
      return "未同步";
  }
}

/** The 进度表's "覆盖范围" cell for one slice: "12 / 480 天", or "—" when unknown. */
export function coverageText(sl: SliceProgress | null): string {
  if (!sl || sl.daysInRange <= 0) return "—";
  return `${sl.daysCovered} / ${sl.daysInRange} 天`;
}

/** The 进度表's per-slice "状态" cell: a source-wide condition (已暂停 /
 * 同步中 / 失败, with the error) overrides every one of its slices;
 * otherwise each slice shows its own coverage state (回填中 / 已完成) — so,
 * for example, "date" can show 已完成 while "query" still shows 回填中. */
export function sliceStateText(src: SourceProgress, sl: SliceProgress | null): string {
  const overrides = src.state === "已暂停" || src.state === "同步中" || src.state === "失败";
  const state = overrides || !sl ? src.state : sl.state;
  return state === "失败" && src.error ? `${state}：${src.error}` : state;
}

/** The 进度表's "下次回填" cell: when a source is 回填中 and the backfill
 * loop has a schedule for it, the time it will next check; "—" otherwise. */
export function nextBackfillText(src: SourceProgress): string {
  return src.state === "回填中" && src.nextBackfillAt ? fmtTime(src.nextBackfillAt) : "—";
}

export type Delta = { text: string; up: boolean } | null;

/** Relative change of a metric where higher is better. */
export function change(cur: number, prev: number): Delta {
  if (!prev) return null;
  const d = ((cur - prev) / prev) * 100;
  return { text: `${d >= 0 ? "+" : ""}${d.toFixed(1)}%`, up: d >= 0 };
}

/** Change of an average position, where lower is better. */
export function posChange(cur: number, prev: number | null): Delta {
  if (prev == null || !cur) return null;
  const d = prev - cur;
  return { text: `${d >= 0 ? "↑" : "↓"}${Math.abs(d).toFixed(1)}`, up: d >= 0 };
}

function niceStep(x: number): number {
  const p = 10 ** Math.floor(Math.log10(x));
  const f = x / p;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
}

/** Round axis bounds with about four steps. */
export function niceScale(min: number, max: number): { lo: number; hi: number; ticks: number[] } {
  const step = niceStep(Math.max(max - min, 4) / 4);
  const lo = Math.floor(min / step) * step;
  const hi = Math.max(Math.ceil(max / step) * step, lo + step);
  const ticks: number[] = [];
  for (let v = lo; v <= hi + step / 2; v += step) ticks.push(v);
  return { lo, hi, ticks };
}

/** The property most likely to cover host: domain property first, then URL prefix. */
export function guessProperty(props: Property[], host: string): string {
  const bare = host.replace(/^www\./, "");
  const want = [`sc-domain:${host}`, `sc-domain:${bare}`, `https://${host}/`, `http://${host}/`];
  for (const w of want) if (props.some((p) => p.siteUrl === w)) return w;
  const loose = props.find((p) => p.siteUrl.includes(bare));
  return loose?.siteUrl ?? props[0]?.siteUrl ?? "";
}

/** Credits with thousands separators and at most one decimal: 3000 → "3,000", 0.5 → "0.5". */
export const fmtCredits = (n: number) => n.toLocaleString("en-US", { maximumFractionDigits: 1 });

export const kindLabel: Record<KeywordKind, string> = { target: "目标词", found: "意外流量", scene: "场景词" };

/** Where and when a keyword number came from, e.g. "Bing 近 4 周 · 2026-11-10" (UTC date). */
export function metricNote(m: Metric): string {
  if (m.value == null || !m.source) return "";
  const src = m.source === "bing" ? "Bing 近 4 周" : "SiteMoment";
  return `${src} · ${new Date(m.at * 1000).toISOString().slice(0, 10)}`;
}

/** Colour class for a KD from 0 to 100: easy, medium, hard. */
export function kdClass(kd: number | null): string {
  if (kd == null) return "";
  return kd < 30 ? "st-good" : kd < 60 ? "st-warn" : "st-bad";
}

const bareHost = (h: string) => h.replace(/^www\./, "").toLowerCase();

/** The GA4 property whose name mentions host, else the first one. */
export function guessGAProperty(props: GAProperty[], host: string): string {
  const bare = bareHost(host);
  return (props.find((p) => p.displayName.toLowerCase().includes(bare)) ?? props[0])?.property ?? "";
}

/** The Bing site that is host (with or without www), else "". */
export function guessBingSite(sites: BingSite[], host: string): string {
  const hostOf = (u: string) => {
    try {
      return bareHost(new URL(u).hostname);
    } catch {
      return "";
    }
  };
  return sites.find((s) => hostOf(s.url) === bareHost(host))?.url ?? "";
}

/**
 * The collector run waiting for a human check, after run changed: a run that
 * needs a human replaces the current one; the current one clears once it
 * moves on (solved, failed or cancelled).
 */
export function nextHumanRun(cur: RunView | null, run: RunView): RunView | null {
  if (run.status === "need_human") return run;
  return cur && cur.id === run.id ? null : cur;
}

/** Local date and time, e.g. "11-10 08:00". */
export function fmtDateTime(unix: number): string {
  const d = new Date(unix * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Local date, e.g. "2026-11-10". */
export function fmtDate(unix: number): string {
  const d = new Date(unix * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** good / warn / bad for a health score: any P0 is bad, below 85 needs attention (mockup 2). */
export function healthState(h: { score: number; p0: number }): "good" | "warn" | "bad" {
  if (h.p0 > 0) return "bad";
  return h.score < 85 ? "warn" : "good";
}

export const healthIcon = { good: "●", warn: "▲", bad: "■" } as const;

export const taskStatusLabel: Record<TaskStatus, string> = {
  proposed: "待确认", open: "进行中", passed: "已通过", failed: "未通过", dismissed: "已忽略",
};

/** An outcome in words: "达标" / "未达标" / "待数据", with the baseline when there is one. */
export function outcomeText(o: Outcome | undefined): string {
  if (!o || !o.state) return "待检查";
  const state = { pass: "达标", unmet: "未达标", pending: "待数据" }[o.state];
  const from = o.baseline != null && o.current != null ? `（开始前 ${round1(o.baseline)} → 现在 ${round1(o.current)}）` : "";
  return state + from;
}

const round1 = (n: number) => String(Math.round(n * 10) / 10);

/** Where a notice ref ("task:3", "report:7", "crawl:2") leads. */
export function noticeTarget(ref: string): "tasks" | "reports" | "health" | null {
  const kind = ref.split(":")[0];
  return kind === "task" ? "tasks" : kind === "report" ? "reports" : kind === "crawl" ? "health" : null;
}
