import { describe, expect, it } from "vitest";
import {
  change, fmtBytes, fmtCompact, fmtCredits, fmtPct, guessBingSite, guessGAProperty, guessProperty, kdClass, metricNote, niceScale, nextHumanRun, posChange,
} from "./format";
import type { RunView } from "./types";

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
