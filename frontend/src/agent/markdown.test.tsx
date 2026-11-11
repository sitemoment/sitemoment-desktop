import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MarkdownView } from "./markdown";

const html = (text: string) => renderToStaticMarkup(<MarkdownView text={text} />);

describe("MarkdownView", () => {
  it("renders headings, lists and GFM tables", () => {
    const out = html("**结论**：可以做\n\n## 依据\n1. GSC 排名 11.8\n\n| 词 | 排名 |\n|---|---|\n| a | 3 |");
    expect(out).toContain("<strong>结论</strong>");
    expect(out).toContain("<h2>依据</h2>");
    expect(out).toContain("<ol>");
    expect(out).toContain("<table>");
  });
  it("drops raw HTML", () => {
    const out = html('before <script>alert(1)</script> <img src="https://x.test/p.png" onerror="alert(1)"> after');
    expect(out).not.toContain("<script");
    expect(out).not.toContain("onerror");
    expect(out).not.toContain("<img");
  });
  it("keeps only http(s) links and never loads images", () => {
    const out = html("[ok](https://example.com) [bad](javascript:alert(1)) ![logo](https://x.test/l.png)");
    expect(out).toContain('href="https://example.com"');
    expect(out).not.toContain("javascript:");
    expect(out).not.toContain("<img");
    expect(out).toContain("[图片：logo]");
  });
});
