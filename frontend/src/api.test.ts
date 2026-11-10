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
