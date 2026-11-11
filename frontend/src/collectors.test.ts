// Runs the built-in collectors (repo-root collectors/) with the real SDK
// (internal/collect/sdk.js) in jsdom, against saved pages.
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

const root = new URL("../../", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, root), "utf8");

interface Posted { run: string; nonce: string; kind: string; data: any }

// run injects collector into a page loaded from html at url and resolves
// with the first message that ends the run. hosts defaults to url's own
// host (the normal case: the page is on an allowed host); tests of the
// SDK's own host guard pass a hosts list that does not include url's host,
// or an http: url, and expect run() to time out (nothing is ever posted,
// because the SDK's first statement returns before the collector body, or
// even the nonce, is touched).
async function run(
  collector: string, html: string, url: string, input: object, setup?: (w: any) => void,
  hosts?: string[], maxWaitMs = 5000,
): Promise<Posted> {
  const dom = new JSDOM(html, { url, runScripts: "outside-only" });
  const w = dom.window as any;
  const posted: Posted[] = [];
  w.webkit = { messageHandlers: { external: { postMessage: (m: string) => posted.push(JSON.parse(m.slice(4))) } } };
  setup?.(w);
  const script = read("internal/collect/sdk.js")
    .replace("__RUN__", () => '"r1"')
    .replace("__NONCE__", () => '"n1"')
    .replace("__HOSTS__", () => JSON.stringify(hosts ?? [new URL(url).hostname]))
    .replace("__INPUT__", () => JSON.stringify(input))
    .replace("__COLLECTOR__", () => read(`collectors/${collector}/collector.js`));
  w.eval(script);
  const step = 25;
  for (let i = 0; i < maxWaitMs / step; i++) {
    const end = posted.find((m) => m.kind !== "log");
    if (end) {
      dom.window.close();
      return end;
    }
    await new Promise((r) => setTimeout(r, step));
  }
  dom.window.close();
  throw new Error("the collector posted nothing");
}

const serpURL = "https://www.google.com/search?q=thumbnail+maker&gl=us&hl=en&num=10";
const serpInput = { q: "thumbnail maker", gl: "us", hl: "en" };

describe("google-serp", () => {
  it("reads a real results page", async () => {
    const m = await run("google-serp", read("collectors/google-serp/testdata/thumbnail-maker.html"), serpURL, serpInput);
    expect(m.kind).toBe("result");
    expect(m.run).toBe("r1");
    const d = m.data;
    expect(d.location).toBe("United States");
    expect(d.aiOverview).toBe(true);
    expect(d.organic).toHaveLength(9);
    expect(d.organic[0]).toMatchObject({ rank: 1, title: "YouTube Thumbnail Maker - Kapwing", url: "", breadcrumb: "www.kapwing.com › Tools › Make" });
    expect(d.organic[0].redirect).toMatch(/^\/url\?opi=/);
    expect(d.organic[0].snippet).toContain("Use Kapwing's online editor");
    expect(d.organic[6]).toMatchObject({
      rank: 7, url: "https://play.google.com/store/apps/details?id=com.sk.thumbnailmaker&hl=en_IN", urlFrom: "redirect", redirect: "",
    });
    expect(d.related).toEqual([
      "YouTube thumbnail maker free", "AI thumbnail maker", "Free AI thumbnail maker", "Automatic thumbnail maker",
      "Thumbnail maker Studio", "Thumbnail maker - channel art", "Ultimate Thumbnail Maker", "Funny thumbnail Maker",
    ]);
    expect(d.paa).toEqual([]);
  });

  it("asks for a human on Google's check page", async () => {
    const m = await run("google-serp", `<form id="captcha-form"></form>`, "https://www.google.com/sorry/index?continue=x", serpInput);
    expect(m.kind).toBe("need_human");
    expect(m.data.reason).toBe("Google 要求人机验证");
  });

  it("reads People also ask and direct links", async () => {
    const html = `<div id="main">
      <div><div><a href="https://www.canva.com/create/thumbnails/"><h3>Free Thumbnail Maker | Canva</h3><div>www.canva.com › create</div></a></div><div>Design thumbnails.</div></div>
      <div><div><a href="https://www.fotor.com/features/youtube-thumbnail/"><h3>YouTube Thumbnail Maker | Fotor</h3></a></div><div>Make thumbnails.</div></div>
      <div><div><span>People also ask</span></div><div><div><span>How do I make a thumbnail for free?</span></div><div><span>What size is a YouTube thumbnail?</span></div></div></div>
    </div>`;
    const m = await run("google-serp", html, serpURL, serpInput);
    expect(m.data.organic[0]).toMatchObject({ url: "https://www.canva.com/create/thumbnails/", urlFrom: "direct", snippet: "Design thumbnails." });
    expect(m.data.paa).toEqual(["How do I make a thumbnail for free?", "What size is a YouTube thumbnail?"]);
  });
});

describe("google-trends", () => {
  it("reads interest over time and related queries through the page's API", async () => {
    const calls: string[] = [];
    const answers: Record<string, unknown> = {
      "/trends/api/explore": { widgets: [
        { id: "TIMESERIES", token: "t1", request: { time: "today 12-m" } },
        { id: "RELATED_QUERIES", token: "t2", request: { keyword: "ai chat" } },
      ] },
      "/trends/api/widgetdata/multiline": { default: { timelineData: [
        { formattedTime: "Nov 9 – 15, 2025", value: [42] }, { formattedTime: "Nov 16 – 22, 2025", value: [100] },
      ] } },
      "/trends/api/widgetdata/relatedsearches": { default: { rankedList: [
        { rankedKeyword: [{ query: "ai chat free", value: 100, formattedValue: "100" }] },
        { rankedKeyword: [{ query: "ai chat bot", value: 250, formattedValue: "+250%" }] },
      ] } },
    };
    const m = await run("google-trends", "<html><body></body></html>", "https://trends.google.com/trends/?geo=US&hl=en-US",
      { q: "ai chat", geo: "US", date: "today 12-m" }, (w) => {
        w.fetch = async (url: string) => {
          calls.push(url);
          const path = url.split("?")[0];
          return { ok: true, status: 200, text: async () => ")]}'\n" + JSON.stringify(answers[path]) };
        };
      });
    expect(m.kind).toBe("result");
    expect(m.data.interest).toEqual([{ time: "Nov 9 – 15, 2025", value: 42 }, { time: "Nov 16 – 22, 2025", value: 100 }]);
    expect(m.data.top).toEqual([{ query: "ai chat free", value: "100" }]);
    expect(m.data.rising).toEqual([{ query: "ai chat bot", value: "+250%" }]);
    expect(calls[0]).toContain("req=%7B%22comparisonItem%22");
    expect(calls[1]).toContain("token=t1");
  });

  it("fails clearly when Trends rate-limits", async () => {
    const m = await run("google-trends", "<html><body></body></html>", "https://trends.google.com/trends/?geo=US",
      { q: "ai chat", geo: "US", date: "today 12-m" }, (w) => {
        w.fetch = async () => ({ ok: false, status: 429, text: async () => "" });
      });
    expect(m.kind).toBe("error");
    expect(m.data.message).toBe("谷歌趋势限流了，稍后再试");
  });
});

describe("the SDK's own host guard", () => {
  // The SDK's very first statement checks location.protocol and
  // location.hostname before touching the nonce or running the collector
  // body, so a page that ends up somewhere the run never declared (a
  // redirect, a stale re-injection after the window moved on) can't do
  // anything with it: nothing is ever posted, so run() times out.
  it("refuses to run on a host the run did not declare", async () => {
    await expect(run("google-serp", "<div id=\"main\"></div>", "https://evil.example.com/search?q=x", serpInput,
      undefined, ["www.google.com"], 200)).rejects.toThrow("the collector posted nothing");
  });

  it("refuses to run over plain http even on an allowed host", async () => {
    await expect(run("google-serp", "<div id=\"main\"></div>", "http://www.google.com/search?q=x", serpInput,
      undefined, ["www.google.com"], 200)).rejects.toThrow("the collector posted nothing");
  });

  it("runs normally once the page is https on a declared host", async () => {
    const html = `<div id="main"><div><a href="https://example.com/"><h3>Example</h3></a></div></div>`;
    const m = await run("google-serp", html, "https://www.google.com/search?q=x", serpInput, undefined, ["www.google.com"], 500);
    expect(m.kind).toBe("result");
  });
});
