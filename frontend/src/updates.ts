import type { UpdateStatus } from "./types";

/** Translates the updater's Go errors for the update bar and Settings. */
export function updateErrorText(msg: string): string {
  if (/staging dir|finalise|promote extracted/.test(msg) && /read-only|permission denied|operation not permitted|access is denied/i.test(msg)) {
    return "SiteMoment 所在的文件夹不能写入。macOS：先把 SiteMoment 拖进「应用程序」文件夹，从那里打开再更新；Windows：用最新的安装包重新安装。";
  }
  if (msg.includes("digest mismatch")) return "下载的文件和 SHA256SUMS 对不上，已丢弃，请稍后重试";
  if (/signature|ed25519/.test(msg)) return "下载的文件签名校验失败，已丢弃";
  if (msg.includes("no SHA-256")) return "这个版本没有提供校验和，不能自动更新，请到 GitHub Releases 手动下载";
  if (/dial tcp|no such host|timeout|connection refused|api 403|rate limit/i.test(msg)) return "连不上 GitHub，请检查网络后重试";
  return msg;
}

/** Download progress in whole percent. */
export const updatePct = (s: UpdateStatus) => (s.total > 0 ? Math.min(100, Math.round((s.written * 100) / s.total)) : 0);

export type UpdateAction = "check" | "install" | "restart";

/** The status line and the one action the update UI offers. */
export function updateView(s: UpdateStatus): { text: string; action?: UpdateAction; busy: boolean } {
  const v = s.latest ? `v${s.latest.version}` : "新版本";
  switch (s.state) {
    case "off":
      return { text: s.reason || "这个版本不自动更新", busy: false };
    case "idle":
      return { text: "还没有检查过更新", action: "check", busy: false };
    case "checking":
      return { text: "正在检查更新…", busy: true };
    case "uptodate":
      return { text: `已是最新版本（v${s.current}）`, action: "check", busy: false };
    case "available":
      return { text: `${v} 可以更新（当前 v${s.current}）`, action: "install", busy: false };
    case "downloading":
      return { text: `正在下载 ${v}… ${updatePct(s)}%`, busy: true };
    case "verifying":
      return { text: `正在校验 ${v}…`, busy: true };
    case "installing":
      return { text: `正在准备 ${v}…`, busy: true };
    case "ready":
      return { text: `${v} 已下载并校验，重启后生效`, action: "restart", busy: false };
    case "error": {
      const what = s.stage === "check" || !s.stage ? "检查更新" : s.stage === "download" ? "下载" : s.stage === "verify" ? "校验" : "安装";
      return { text: `${what}失败：${updateErrorText(s.error || "")}`, action: s.stage === "check" || !s.stage ? "check" : "install", busy: false };
    }
  }
}

/** Whether the update bar shows (Settings always shows the status). */
export const updateBarVisible = (s: UpdateStatus | null) =>
  !!s && (["available", "downloading", "verifying", "installing", "ready"].includes(s.state) || (s.state === "error" && !!s.stage && s.stage !== "check"));
