import { describe, expect, it } from "vitest";
import type { Probe, ProxyStatus } from "./types";
import { exceptionsText, probeText, proxySummary } from "./network";

const base: ProxyStatus = { source: "direct", httpProxy: "", httpsProxy: "", socks: "", exceptions: [], pac: "", error: "" };

describe("proxySummary", () => {
  it("names the system proxy in use", () => {
    expect(proxySummary({ ...base, source: "system", httpProxy: "127.0.0.1:7890", httpsProxy: "127.0.0.1:7890", socks: "127.0.0.1:7891" }))
      .toEqual({ text: "跟随系统代理：HTTP 127.0.0.1:7890", tone: "good" });
    expect(proxySummary({ ...base, source: "system", httpProxy: "127.0.0.1:7890", httpsProxy: "127.0.0.1:7891" }).text)
      .toBe("跟随系统代理：HTTP 127.0.0.1:7890 · HTTPS 127.0.0.1:7891");
    expect(proxySummary({ ...base, source: "system", httpsProxy: "127.0.0.1:7890" }).text).toBe("跟随系统代理：HTTPS 127.0.0.1:7890");
    expect(proxySummary({ ...base, source: "system", socks: "127.0.0.1:7891" }).text).toBe("跟随系统代理：SOCKS5 127.0.0.1:7891");
  });
  it("explains environment, PAC and direct", () => {
    expect(proxySummary({ ...base, source: "env", httpProxy: "http://127.0.0.1:1081", httpsProxy: "http://127.0.0.1:1081" }))
      .toEqual({ text: "系统没有设置代理，使用环境变量里的代理：http://127.0.0.1:1081", tone: "good" });
    expect(proxySummary({ ...base, source: "env", socks: "socks5://127.0.0.1:7891" }).text)
      .toBe("系统没有设置代理，使用环境变量里的代理：socks5://127.0.0.1:7891");
    expect(proxySummary({ ...base, source: "env", httpsProxy: "http://h:1", pac: "http://127.0.0.1/pac" }).text)
      .toBe("系统使用 PAC 自动配置，暂不支持，改用环境变量里的代理：http://h:1");
    expect(proxySummary({ ...base, source: "pac-unsupported", pac: "http://127.0.0.1:33331/pac" }))
      .toEqual({ text: "系统使用 PAC 自动配置，暂不支持，现在直连。请在代理软件里改用「系统代理」（不是 PAC 模式）。", tone: "warn" });
    expect(proxySummary(base)).toEqual({ text: "直连（系统没有设置代理）", tone: "plain" });
    expect(proxySummary({ ...base, error: "exit status 1" })).toEqual({ text: "直连（读取系统代理失败：exit status 1）", tone: "warn" });
  });
});

describe("exceptionsText", () => {
  it("lists a few and counts the rest", () => {
    expect(exceptionsText({ ...base, exceptions: null })).toBe("");
    expect(exceptionsText({ ...base, exceptions: ["localhost", "*.local"] })).toBe("不走代理：localhost、*.local");
    expect(exceptionsText({ ...base, exceptions: ["a", "b", "c", "d", "e", "f", "g"] })).toBe("不走代理：a、b、c、d、e 等 7 项");
  });
});

describe("probeText", () => {
  const p: Probe = { url: "https://www.google.com/generate_204", ok: true, status: 204, ms: 312, via: "HTTP 127.0.0.1:7890", error: "" };
  it("reports latency or the reason", () => {
    expect(probeText(p)).toBe("www.google.com：通了，312 ms（经 HTTP 127.0.0.1:7890）");
    expect(probeText({ ...p, via: "直连" })).toBe("www.google.com：通了，312 ms（直连）");
    expect(probeText({ ...p, ok: false, status: 0, error: "超过 10 秒没有响应" })).toBe("www.google.com：不通，超过 10 秒没有响应（经 HTTP 127.0.0.1:7890）");
  });
});
