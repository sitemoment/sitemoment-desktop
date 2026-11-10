import type { Property } from "./types";

export const fmtInt = (n: number) => Math.round(n).toLocaleString("en-US");

export function fmtCompact(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e4) return (n / 1e3).toFixed(1) + "K";
  return fmtInt(n);
}

export const fmtPct = (ratio: number) => (ratio * 100).toFixed(2) + "%";

export function fmtBytes(b: number): string {
  return b >= 1 << 30 ? (b / (1 << 30)).toFixed(1) + " GB" : (b / (1 << 20)).toFixed(1) + " MB";
}

export function fmtTime(unix: number): string {
  return new Date(unix * 1000).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
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
