import { describe, expect, it } from "vitest";
import {
  change, coverageText, fmtBytes, fmtCompact, fmtCredits, fmtDate, fmtDateTime, fmtPct, guessBingSite, guessGAProperty, guessProperty, healthState,
  kdClass, metricNote, niceScale, nextHumanRun, noticeTarget, outcomeText, posChange, sourceStateText, syncStatusLabel,
} from "./format";
import type { RunView, SliceProgress, SourceProgress, SyncStatus } from "./types";

describe("format", () => {
  it("compacts big numbers", () => {
    expect(fmtCompact(486200)).toBe("486.2K");
    expect(fmtCompact(12309)).toBe("12.3K");
    expect(fmtCompact(9876)).toBe("9,876");
    expect(fmtCompact(2_500_000)).toBe("2.5M");
    expect(fmtPct(0.02571)).toBe("2.57%");
  });
  it("compacts numbers near a unit boundary without overflowing the unit", () => {
    expect(fmtCompact(9_999)).toBe("9,999");
    expect(fmtCompact(10_000)).toBe("10.0K");
    expect(fmtCompact(999_949)).toBe("999.9K");
    expect(fmtCompact(999_999)).toBe("1.0M");
  });
  it("formats byte counts below a megabyte as KB", () => {
    expect(fmtBytes(524288)).toBe("512.0 KB");
  });
  it("compares periods", () => {
    expect(change(120, 100)).toEqual({ text: "+20.0%", up: true });
    expect(change(90, 100)).toEqual({ text: "-10.0%", up: false });
    expect(change(5, 0)).toBeNull();
  });
  it("treats a lower position as better", () => {
    expect(posChange(11.8, 14.8)).toEqual({ text: "↑3.0", up: true });
    expect(posChange(9.3, 8.5)).toEqual({ text: "↓0.8", up: false });
    expect(posChange(5, null)).toBeNull();
  });
  it("picks round axis ticks", () => {
    expect(niceScale(312, 515)).toEqual({ lo: 300, hi: 600, ticks: [300, 400, 500, 600] });
    expect(niceScale(0, 0)).toEqual({ lo: 0, hi: 1, ticks: [0, 1] });
  });
  it("guesses the Search Console property for a host", () => {
    const props = [
      { siteUrl: "https://blog.example.com/", permissionLevel: "siteOwner" },
      { siteUrl: "sc-domain:example.com", permissionLevel: "siteOwner" },
    ];
    expect(guessProperty(props, "www.example.com")).toBe("sc-domain:example.com");
    expect(guessProperty(props, "blog.example.com")).toBe("https://blog.example.com/");
    expect(guessProperty(props, "other.org")).toBe("https://blog.example.com/");
    expect(guessProperty([], "other.org")).toBe("");
  });
});

describe("M2 formatting", () => {
  it("formats credits without useless decimals", () => {
    expect(fmtCredits(312)).toBe("312");
    expect(fmtCredits(3000)).toBe("3,000");
    expect(fmtCredits(0.5)).toBe("0.5");
    expect(fmtCredits(1234.5)).toBe("1,234.5");
  });
  it("notes where a keyword number came from", () => {
    expect(metricNote({ value: 1900, source: "sitemoment", at: 1790000000 })).toBe("SiteMoment · 2026-09-21");
    expect(metricNote({ value: 180, source: "bing", at: 1790000000 })).toBe("Bing 近 4 周 · 2026-09-21");
    expect(metricNote({ value: null, source: "", at: 0 })).toBe("");
  });
  it("colours KD by difficulty", () => {
    expect(kdClass(12)).toBe("st-good");
    expect(kdClass(45)).toBe("st-warn");
    expect(kdClass(80)).toBe("st-bad");
    expect(kdClass(null)).toBe("");
  });
  it("guesses the GA4 property and Bing site for a host", () => {
    const props = [
      { property: "properties/1", displayName: "Other", account: "accounts/1", accountName: "A" },
      { property: "properties/2", displayName: "example.com - GA4", account: "accounts/1", accountName: "A" },
    ];
    expect(guessGAProperty(props, "www.example.com")).toBe("properties/2");
    expect(guessGAProperty(props, "nothing.org")).toBe("properties/1");
    expect(guessGAProperty([], "x.org")).toBe("");
    const sites = [{ url: "https://blog.example.com/", verified: true }, { url: "https://www.example.com/", verified: true }];
    expect(guessBingSite(sites, "example.com")).toBe("https://www.example.com/");
    expect(guessBingSite(sites, "blog.example.com")).toBe("https://blog.example.com/");
    expect(guessBingSite(sites, "other.org")).toBe("");
  });
});

describe("nextHumanRun", () => {
  const run = (id: number, status: RunView["status"]): RunView => ({ id, collector: "google-serp", input: "{}", status, error: "", createdAt: 1, finishedAt: 0 });
  it("shows a run that needs a human", () => {
    expect(nextHumanRun(null, run(1, "need_human"))?.id).toBe(1);
  });
  it("clears it once that run moves on", () => {
    expect(nextHumanRun(run(1, "need_human"), run(1, "ok"))).toBeNull();
    expect(nextHumanRun(run(1, "need_human"), run(1, "error"))).toBeNull();
  });
  it("keeps it when another run changes", () => {
    expect(nextHumanRun(run(1, "need_human"), run(2, "running"))?.id).toBe(1);
  });
});

describe("health, tasks and notices", () => {
  it("grades a health score", () => {
    expect(healthState({ score: 92, p0: 0 })).toBe("good");
    expect(healthState({ score: 78, p0: 0 })).toBe("warn");
    expect(healthState({ score: 90, p0: 1 })).toBe("bad");
  });
  it("describes an outcome", () => {
    expect(outcomeText(undefined)).toBe("待检查");
    expect(outcomeText({ state: "unmet", current: 12.04, baseline: 14, note: "", at: 1 })).toBe("未达标（开始前 14 → 现在 12）");
    expect(outcomeText({ state: "pending", note: "", at: 1 })).toBe("待数据");
  });
  it("routes notices", () => {
    expect(noticeTarget("task:3")).toBe("tasks");
    expect(noticeTarget("report:7")).toBe("reports");
    expect(noticeTarget("crawl:2")).toBe("health");
    expect(noticeTarget("")).toBeNull();
  });
  it("formats local dates", () => {
    const t = new Date(2026, 10, 10, 8, 5).getTime() / 1000;
    expect(fmtDateTime(t)).toBe("11-10 08:05");
    expect(fmtDate(t)).toBe("2026-11-10");
  });
  it("labels the status bar's sync state, paused beating everything else", () => {
    // Built from a local wall-clock time, like fmtDateTime's test above, so
    // the expectation does not depend on the host's time zone.
    const at = Math.floor(new Date(2026, 10, 10, 8, 0).getTime() / 1000);
    const running: SyncStatus = { siteId: 1, state: "running", requests: 0, at };
    const failed: SyncStatus = { siteId: 1, state: "failed", error: "Google 接口限流，稍后自动重试", requests: 0, at };
    const stopped: SyncStatus = { siteId: 1, state: "stopped", requests: 0, at };
    const done: SyncStatus = { siteId: 1, state: "done", requests: 3, at };

    expect(syncStatusLabel(undefined, false, null)).toBe("未同步");
    expect(syncStatusLabel(running, false, null)).toBe("同步中");
    expect(syncStatusLabel(running, false, { siteId: 1, slice: "query", day: "2026-11-09", done: 3, total: 7 })).toBe("同步中 query 3/7");
    expect(syncStatusLabel(failed, false, null)).toBe("同步失败：Google 接口限流，稍后自动重试");
    expect(syncStatusLabel(stopped, false, null)).toBe("已停止同步");
    expect(syncStatusLabel(done, false, null)).toBe("● 已同步 08:00");
    // Paused overrides every other state, including a run still "running".
    expect(syncStatusLabel(running, true, null)).toBe("已暂停");
  });
  it("shows a slice's days-covered coverage, or an em dash when unknown", () => {
    const sl: SliceProgress = { slice: "query", label: "查询词", daysCovered: 90, daysInRange: 480, state: "回填中" };
    expect(coverageText(sl)).toBe("90 / 480 天");
    expect(coverageText(null)).toBe("—");
    expect(coverageText({ ...sl, daysInRange: 0 })).toBe("—");
  });
  it("appends a next-backfill hint or an error to a source's progress state", () => {
    const at = Math.floor(new Date(2026, 10, 10, 8, 0).getTime() / 1000);
    const backfilling: SourceProgress = { source: "gsc", label: "Search Console", state: "回填中", nextBackfillAt: at };
    const failed: SourceProgress = { source: "gsc", label: "Search Console", state: "失败", error: "Google 授权已失效，请在设置里重新连接" };
    const done: SourceProgress = { source: "gsc", label: "Search Console", state: "已完成" };

    expect(sourceStateText(backfilling)).toBe("回填中（约 08:00 继续）");
    expect(sourceStateText(failed)).toBe("失败：Google 授权已失效，请在设置里重新连接");
    expect(sourceStateText(done)).toBe("已完成");
  });
});
