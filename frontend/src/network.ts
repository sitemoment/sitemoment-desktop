import type { Probe, ProxyStatus } from "./types";

export type Tone = "good" | "warn" | "plain";

/** One line for Settings → 网络代理: which proxy requests go through. */
export function proxySummary(s: ProxyStatus): { text: string; tone: Tone } {
  switch (s.source) {
    case "system": {
      const parts: string[] = [];
      if (s.httpProxy) parts.push(`HTTP ${s.httpProxy}`);
      if (s.httpsProxy && s.httpsProxy !== s.httpProxy) parts.push(`HTTPS ${s.httpsProxy}`);
      // SOCKS is only used when there is no web proxy (netproxy.System.proxyFor).
      if (parts.length === 0 && s.socks) parts.push(`SOCKS5 ${s.socks}`);
      return { text: `跟随系统代理：${parts.join(" · ")}`, tone: "good" };
    }
    case "env": {
      const v = [...new Set([s.httpProxy, s.httpsProxy, s.socks].filter(Boolean))].join(" · ");
      const lead = s.pac ? "系统使用 PAC 自动配置，暂不支持，改用环境变量里的代理：" : "系统没有设置代理，使用环境变量里的代理：";
      return { text: lead + v, tone: "good" };
    }
    case "pac-unsupported":
      return { text: "系统使用 PAC 自动配置，暂不支持，现在直连。请在代理软件里改用「系统代理」（不是 PAC 模式）。", tone: "warn" };
    default:
      if (s.error) return { text: `直连（读取系统代理失败：${s.error}）`, tone: "warn" };
      return { text: "直连（系统没有设置代理）", tone: "plain" };
  }
}

/** The hosts that skip the proxy: the first few and a count. */
export function exceptionsText(s: ProxyStatus): string {
  const ex = s.exceptions ?? [];
  if (ex.length === 0) return "";
  const shown = ex.slice(0, 5).join("、");
  return ex.length > 5 ? `不走代理：${shown} 等 ${ex.length} 项` : `不走代理：${shown}`;
}

/** One 「测试连接」 result. */
export function probeText(p: Probe): string {
  let host = p.url;
  try { host = new URL(p.url).host; } catch { /* keep the raw URL */ }
  const via = p.via === "直连" ? "（直连）" : `（经 ${p.via}）`;
  return p.ok ? `${host}：通了，${p.ms} ms${via}` : `${host}：不通，${p.error}${via}`;
}
