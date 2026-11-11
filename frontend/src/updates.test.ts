import { describe, expect, it } from "vitest";
import type { UpdateStatus } from "./types";
import { updateBarVisible, updateErrorText, updatePct, updateView } from "./updates";

const base: UpdateStatus = { current: "0.5.0", state: "idle", written: 0, total: 0, checkedAt: 0, auto: true };
const latest = { version: "0.6.0", notes: "- x", url: "https://github.com/x", publishedAt: 1, size: 10 };

describe("updateView", () => {
  it("offers one action per state", () => {
    expect(updateView({ ...base, state: "off", reason: "开发版本（dev）不自动更新" })).toEqual({ text: "开发版本（dev）不自动更新", busy: false });
    expect(updateView(base).action).toBe("check");
    expect(updateView({ ...base, state: "checking" }).busy).toBe(true);
    expect(updateView({ ...base, state: "uptodate" }).text).toBe("已是最新版本（v0.5.0）");
    expect(updateView({ ...base, state: "available", latest })).toEqual({ text: "v0.6.0 可以更新（当前 v0.5.0）", action: "install", busy: false });
    expect(updateView({ ...base, state: "downloading", latest, written: 5, total: 10 }).text).toBe("正在下载 v0.6.0… 50%");
    expect(updateView({ ...base, state: "ready", latest }).action).toBe("restart");
  });
  it("retries the failed step", () => {
    expect(updateView({ ...base, state: "error", stage: "check", error: "dial tcp: i/o timeout" })).toEqual({
      text: "检查更新失败：连不上 GitHub，请检查网络后重试", action: "check", busy: false,
    });
    const v = updateView({ ...base, state: "error", stage: "verify", error: "updater: digest mismatch", latest });
    expect(v.action).toBe("install");
    expect(v.text).toContain("校验失败");
  });
});

describe("helpers", () => {
  it("explains a folder the app cannot write to", () => {
    expect(updateErrorText("updater: staging dir: mkdir /Volumes/SiteMoment/wails-update-1: read-only file system")).toContain("应用程序");
    expect(updateErrorText("updater: staging dir: open C:\\Program Files\\x: Access is denied.")).toContain("安装包");
    expect(updateErrorText("something else")).toBe("something else");
  });
  it("computes progress and when the bar shows", () => {
    expect(updatePct({ ...base, written: 3, total: 4 })).toBe(75);
    expect(updatePct({ ...base, written: 3, total: 0 })).toBe(0);
    expect(updateBarVisible(null)).toBe(false);
    expect(updateBarVisible({ ...base, state: "uptodate" })).toBe(false);
    expect(updateBarVisible({ ...base, state: "available", latest })).toBe(true);
    expect(updateBarVisible({ ...base, state: "error", stage: "check" })).toBe(false);
    expect(updateBarVisible({ ...base, state: "error", stage: "download" })).toBe(true);
  });
});
