import { describe, expect, it } from "vitest";
import { change, fmtCompact, fmtPct, guessProperty, niceScale, posChange } from "./format";

describe("format", () => {
  it("compacts big numbers", () => {
    expect(fmtCompact(486200)).toBe("486.2K");
    expect(fmtCompact(12309)).toBe("12.3K");
    expect(fmtCompact(9876)).toBe("9,876");
    expect(fmtCompact(2_500_000)).toBe("2.5M");
    expect(fmtPct(0.02571)).toBe("2.57%");
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
