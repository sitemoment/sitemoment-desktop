import { describe, expect, it } from "vitest";
import { errText, isNetworkError } from "./api";

describe("errText", () => {
  it("translates a cancelled Google login", () => {
    expect(errText(new Error("oauthx: authorization failed: access_denied"))).toBe("已取消登录");
  });
  it("translates an oauthx state mismatch", () => {
    expect(errText(new Error("oauthx: state mismatch"))).toBe("登录请求不匹配，请重试");
  });
  it("translates google.ErrScopes", () => {
    expect(errText(new Error("google: Search Console access was not granted"))).toBe(
      "没有授予 Search Console 读取权限，请重新连接并勾选",
    );
  });
  it("translates google.ErrReauth", () => {
    expect(errText(new Error("google: authorization expired, connect the account again"))).toBe(
      "Google 授权已失效，请重新连接",
    );
  });
  it("translates network failures", () => {
    expect(errText(new Error("dial tcp 127.0.0.1:443: connect: connection refused"))).toBe("网络连接失败，请检查网络");
    expect(errText(new Error("lookup sitemoment.com: no such host"))).toBe("网络连接失败，请检查网络");
    expect(errText(new Error("read tcp 127.0.0.1:443: i/o timeout"))).toBe("网络连接失败，请检查网络");
  });
  it("passes through an unrecognized message unchanged", () => {
    expect(errText(new Error("gscsync: site has no Search Console property"))).toBe(
      "gscsync: site has no Search Console property",
    );
  });
  it("translates Bing, Analytics, sign-in and keyword errors", () => {
    expect(errText(new Error("这不像是 Bing Webmaster 的 API key: bing: the API key is not valid"))).toBe("Bing API key 无效，请检查后重新添加");
    expect(errText(new Error("bing: Bing Webmaster is throttling this key, try again later"))).toBe("Bing 暂时限流，请稍后再试");
    expect(errText(new Error("bing: this key cannot read that site"))).toBe("这个 Bing key 读不到该站点");
    expect(errText(new Error("google: Analytics access was not granted, connect the account again and allow it"))).toBe(
      "没有授予 Google Analytics 读取权限，请重新连接 Google 账号并勾选",
    );
    expect(errText(new Error("cloud: not signed in"))).toBe("请先登录 SiteMoment");
    expect(errText(new Error("store: a keyword is 1 to 200 characters"))).toBe("关键词不能为空，最长 200 个字符");
  });
  it("translates paid data API error codes", () => {
    expect(errText(new Error("cloud: HTTP 402 credits_exhausted: not enough credits"))).toBe("本月额度已用完");
    expect(errText(new Error("cloud: HTTP 400 request_too_large: 这次请求太大，请减少关键词数量或 limit"))).toBe("这次请求太大，请减少关键词数量");
    expect(errText(new Error("cloud: SiteMoment data is not available right now (data_budget)"))).toBe("数据服务今天的总额度已用完，请明天再试");
  });
  it("translates the 429 rate-limit error codes", () => {
    expect(errText(new Error("cloud: HTTP 429 rate_limited: too many SERP lookups today"))).toBe("今天的数据请求次数已用完，请明天再试");
    expect(errText(new Error("cloud: HTTP 429 too_many_requests: slow down"))).toBe("请等上一个取数完成");
  });
  it("tells hosted-model credit exhaustion apart", () => {
    expect(errText(new Error("llm: HTTP 402: This month's SiteMoment credits cannot cover this call (credits_exhausted)"))).toBe(
      "本月 SiteMoment 额度已用完，可以在设置 → AI 模型里改用自己的 key",
    );
  });
  it("passes through the model's own truncation and interruption messages", () => {
    expect(errText(new Error("llm: 模型回复太长，已中止"))).toBe("模型回复太长，已中止");
    expect(errText(new Error("llm: 模型回复中断"))).toBe("模型回复中断");
  });
  it("stringifies a non-Error value", () => {
    expect(errText("plain string")).toBe("plain string");
  });
});

describe("isNetworkError", () => {
  it("matches dial/host/timeout failures only", () => {
    expect(isNetworkError("dial tcp: connection refused")).toBe(true);
    expect(isNetworkError("no such host")).toBe(true);
    expect(isNetworkError("request timeout")).toBe(true);
    expect(isNetworkError("google: Search Console HTTP 500")).toBe(false);
  });
});
