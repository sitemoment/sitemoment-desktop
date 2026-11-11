import { openURL } from "../api";

export const PRIVACY_URL = "https://sitemoment.com/seo/privacy";

/** Settings → 隐私说明: what stays on this computer and what leaves it. */
export function PrivacyPanel() {
  return (
    <section className="panel privacy">
      <h2>隐私说明</h2>
      <p>SiteMoment Desktop 是本地优先的：你的站点、Search Console / GA4 / Bing 数据和各种授权都保存在这台电脑上。没有统计、遥测或崩溃上报。</p>
      <details>
        <summary>保存在本机的</summary>
        <ul>
          <li>站点、同步下来的报表、关键词、体检、任务、报告、Agent 对话、采集结果：存在数据目录里的 SQLite 数据库。数据库不加密，依靠系统的磁盘加密（FileVault、BitLocker）。</li>
          <li>Google refresh token、Bing API key、你自己的模型 key、SiteMoment 登录令牌：存在系统钥匙串（macOS 钥匙串、Windows 凭据管理器），不写数据库、不写日志。</li>
          <li>注意：macOS 上应用通过系统的 <code>/usr/bin/security</code> 读写钥匙串，同一用户下的其它程序不需要弹窗确认也能读到这些条目；Windows 上同一用户的程序也能读凭据管理器。请把你的系统账号当作安全边界。</li>
        </ul>
      </details>
      <details>
        <summary>发给 SiteMoment 的（登录后）</summary>
        <ul>
          <li><b>托管模型</b>：你的提问、对话和 Agent 带上的数据（可能包括它查出的 Search Console 数据）经 SiteMoment 服务器转发给模型服务商，<b>只转发、不保存内容</b>，只记录时间、模型、token 数和扣的积分。用自己的模型 key 时，直接从这台电脑发给你选的服务商，不经过 SiteMoment。</li>
          <li><b>付费 SEO 数据</b>：只发送这次查询的关键词、域名或网址，<b>不发送 Search Console 或 GA 数据</b>。结果放在不和账号关联的共享缓存里，你的积分记录只有工具名、积分和时间。</li>
          <li>Google 数据不出售、不用于广告、不用于训练任何 AI 模型。</li>
        </ul>
      </details>
      <details>
        <summary>应用还会访问的</summary>
        <ul>
          <li>Google、Bing 的接口：用你自己的授权，从这台电脑直接访问。</li>
          <li>采集器：用隐藏的浏览器窗口从你自己的网络打开公开网页（如 Google 搜索结果），对方能看到你的 IP；遇到验证码时由你处理，应用不会自动处理。</li>
          <li>你让它抓取的网站、Google PageSpeed、Google 搜索联想、Wayback Machine。</li>
          <li>GitHub：每天一次（和你点「检查更新」时）查询新版本、下载更新。</li>
        </ul>
      </details>
      <div className="row">
        <button className="btn" onClick={() => openURL(PRIVACY_URL)}>完整隐私政策（sitemoment.com）</button>
        <button className="btn" onClick={() => openURL("https://myaccount.google.com/permissions")}>管理 Google 授权</button>
      </div>
    </section>
  );
}
