# SiteMoment Desktop

开源的本地 SEO 运营客户端：多个站点的 Search Console、GA4、Bing 数据保存在你自己的电脑上，SEO Agent 在本机分析。

- 许可证：AGPL-3.0-or-later
- 设计文档：sitemoment 仓库 `docs/superpowers/specs/2026-11-10-sitemoment-desktop-design.md`

## 当前状态（2026-11-10）

**M0 结论**（详见 [`docs/spikes/2026-11-m0-collector.md`](docs/spikes/2026-11-m0-collector.md)）：引导页 → `wails:runtime:ready` → `SetURL` → 导航完成 → `ExecJS` → `RawMessageHandler` → `collect.Verify` 这条传输链路在 macOS 上走通，包括连续多次导航后 `ExecJS` 仍然生效。Windows 未测（没有 Windows 机器）。Google 搜索结果页的 DOM 已变化，相关搜索修好了，但 organic 结果的落地页 URL 大多提取不出来（`/url?q=` 变成了不透明 token，也没有 `<cite>` 元素），M3 真正做采集器时需要重新解决。

**M1 已完成的功能**（分支 `m0-m1`，后端改动在仓库 `sitemoment` 的分支 `desktop-auth`）：
- SiteMoment 账号登录：浏览器打开站内确认页，「继续」后换取 access/refresh token；refresh token 存在系统钥匙串（服务名 `SiteMoment`），退出重进保持登录。隐私提醒：macOS 上 go-keyring 通过 `/usr/bin/security` 存取钥匙串项，同一用户下的其它本地进程不需要弹窗确认也能读到。
- 多站点：添加、列表、删除。
- 连接 Google 账号（OAuth，PKCE + 回环端口），资源列表拉取和匹配。
- 绑定 Search Console 资源并同步 16 个月数据，同步状态（`同步中 query n/m` / `已完成` / `失败`）。
- 概览页：4 个 KPI、按天点击图（自写 SVG 组件，悬停显示数值）、查询词表，7d / 28d / 90d 切换。
- 深色控制台风格界面（CSS 变量主题，`frontend/src/theme.css`）。
- 本地 SQLite 存储（`modernc.org/sqlite`），业务逻辑都在不依赖 Wails 的包里，单测覆盖。

**M2 已完成的功能**（分支 `m2`，后端改动在仓库 `sitemoment` 的分支 `desktop-m2`）：
- GA4：用同一个 Google 授权列出媒体资源（Admin API `accountSummaries`），每个站点绑定一个媒体资源，按天同步会话、用户、互动会话、关键事件，以及着陆页和默认渠道组，回填 16 个月，最近 3 天每 6 小时重拉。
- Bing Webmaster：在设置里粘贴 API key（存系统钥匙串，名字 `bing-<后4位>`），每个站点绑定一个 Bing 站点；每 12 小时整体刷新每天的流量和按周的查询、页面数据（Bing 只提供最近几个月）。
- 概览页多出 GA4「会话」和 Bing「点击」KPI；底部「数据源」面板绑定 / 更换 / 解绑 GA4 和 Bing。
- 关键词工作台：目标词、场景词手动添加，「意外流量」自动取 Search Console 最近 28 天展示量前 200 的查询；每个词显示 GSC 平均排名和展示；「取数 · Bing」用 Bing 关键词数据填搜索量（免费，近 4 周精确匹配）；「取数 · SiteMoment」先报价再确认，用 SiteMoment 数据（web.cafe `lookup_keywords`）填搜索量、KD、CPC，扣本月积分。每个数字都记录来源和日期，付费数据不会被 Bing 数据覆盖。服务端认为某次请求无论如何都会超过单次调用预算时，会在报价和目录里标 `tooLarge`；客户端据此直接拒绝，不会弹出「确认扣积分」的对话框。
- 状态栏显示本月 SiteMoment 额度（已用 / 总额）。

**M2 已知问题 / 待办**：
- web.cafe 没有公开 `lookup_keywords` 的响应结构，客户端按常见字段名宽松解析（`internal/cloud/keywords.go`）。第一次真实取数后如果搜索量或 KD 仍显示「—」，在后端数据库看原始响应：`SELECT data FROM seo_cache WHERE endpoint = 'lookup_keywords' ORDER BY fetched_at DESC LIMIT 1`，把实际字段名加进 `volumeKeys` / `kdKeys` / `cpcKeys`。
- 关键词市场固定为美国 / 英语（`internal/app/keywords.go` 的常量），以后做成站点设置。
- Bing 的历史保留时长、限流阈值 Microsoft 没有公开，以实际返回为准。
- [desktop] 相关的积分额度（免费版 / 付费版每月多少积分）是临时数值，还没有和负责人最终确认。

**M3 已完成的功能**（分支 `m3`，后端改动在仓库 `sitemoment` 的分支 `desktop-m3`）：
- 右侧 SEO Agent 面板（标题栏「Agent」按钮收起 / 展开）：每个站点多个会话，回答流式显示（Markdown，不渲染原始 HTML，链接在系统浏览器打开），工具调用显示成卡片（本地数据 / 免费网页 / 采集器 / SiteMoment 数据），停止按钮，花钱模式（只用免费 / 付费前确认 / 自动，自动模式每轮最多 20 积分）。付费数据在「先确认」模式下弹出确认条，写明最多扣多少积分和本月剩余。
- 模型（设置 → AI 模型）：SiteMoment 托管（登录后可用，按 token 扣本月积分：每 1,000 token 1 积分，精确到 0.01）、OpenAI 兼容接口（base URL + key + 模型名）、Anthropic（官方 Go SDK，默认 `claude-opus-5`）。key 只存系统钥匙串。模型不支持工具调用时自动改用 JSON 协议。
- Agent 工具：本地 GSC / GA4 / Bing / 关键词查询；站点体检、抓取网页、网址状态、旧网址检查、页面速度、谷歌联想、方法手册；采集器（谷歌搜索结果、谷歌趋势、People Also Ask）；SiteMoment 数据（web.cafe 的 13 个接口 + 付费谷歌搜索结果）。
- 采集器：隐藏的 WebView 窗口（最多 2 个）打开网页、注入 `collectors/<id>/collector.js`，结果经原生消息通道回传并核对来源和 nonce；谷歌搜索每次间隔 ≥10 秒、每天最多 200 次、缓存 7 天；遇到验证码时弹出窗口并在主窗口顶部提示，2 分钟内没人处理就失败（Agent 会建议改用付费结果）。谷歌结果页的不透明跳转链接由 Go 逐条 GET 解析成真实网址，解析不出来时退回到面包屑里的站点。
- 采集器页：内置和用户自己的采集器（放进数据目录的 `collectors/` 后「重新扫描」，第一次使用前要确认它访问的域名）、最近运行记录、需要人工处理的队列；状态栏显示采集队列和今天的次数。
- 安全：采集窗口里的网页不能访问应用的资源和 `/wails/runtime`（`internal/webview.Middleware` 按窗口名返回 403）；应用事件只发给主窗口。

**M3 已知问题 / 待办**：
- Windows 上没有测过（采集窗口、导航事件、WebView2 的消息通道）。
- 谷歌趋势采集器用的是 trends.google.com 网页自己调用的接口（`/trends/api/explore`、`widgetdata/multiline`、`widgetdata/relatedsearches`），形状只用桩数据测过；People Also Ask 在实测的结果页上没有出现，规则只用合成页面测过。第一次真实使用时核对，不对就改 `collectors/google-trends/collector.js`、`collectors/google-serp/collector.js` 并更新测试样本。
- 谷歌对刚搜完就连续解析跳转链接会限流（200 + 几秒后的 `/goto` 跳转），解析器一条一条、限流时等 10 秒重试一次；第二轮重试的效果没有在真实页面上验证过。
- 系统通知（macOS 通知中心）没做：需要签名的 app 包，放到 M5。
- Anthropic 的 server-side fallbacks（beta）没有开，模型拒答时直接报错。
- 托管模型需要后端 `desktop-m3` 部署后才能用（走 DevOpsPad，需要负责人同意）。

**M4 已完成的功能**（分支 `m4`，没有后端改动）：
- 每日体检：每个站点每天本地 08:00 自动体检一次（应用没开时，下次打开就补跑；也可以在体检页或概览的体检面板「立即体检」）。爬最多 300 页，遵守 robots.txt（按 `SiteMomentBot` 选组；robots.txt 返回 5xx 时不抓，并报 P0），用 SEO 审计规则查问题，再和上次体检对比：以前正常的页面现在报错（P0，附 301 建议）、新出现的 noindex（P0）、canonical 变了（P1）、GSC 最近 28 天点击或展示比前 28 天跌 30% 以上（P1，本机数据），以及谷歌没有收录的页面（P1，Search Console 网址检查：只查可疑页面，每次最多 50 个、每站每天最多 200 个）。问题分 P0/P1/P2，健康分 = 100 − 25×P0 − 8×P1 − 2×P2（按问题种类）。体检页有问题列表、记住的页面（筛选报错 / noindex / 跳转 / 未收录）和体检记录；站点列表显示健康分。
- 任务：Agent 用「创建任务」把建议变成任务，验收条件可以是关键词的 GSC 平均排名、网址返回的状态码、页面是否被谷歌收录、全站 / 页面 / 关键词的 GSC 指标。任务页确认后开始计时（或者打开「Agent 建议的任务直接开始」），每次同步和体检后自动复查，GSC 条件只统计开始之后的数据并显示开始前的基线；全部达标就通过，到期没达标就是未通过。
- 周报：每个站点每 7 天自动生成一份（数据全部来自本机：GSC、GA4、Bing、体检、任务），也可以「现在生成」；报告页里预览，导出 HTML（单文件，图表是内联 SVG）或 Markdown。
- 应用内通知（状态栏「通知」）：体检发现新的 P0、体检失败、Agent 建议了任务、任务通过 / 到期未通过、周报生成；点击跳到对应页面。
- Agent 新工具：体检报告、站点页面、创建任务、任务列表。

**M4 已知问题 / 待办**：
- 系统通知（macOS 通知中心）要签名的 app 包，放到 M5；现在只有应用内通知。
- 任务只能由 Agent 建，没有手工新建的表单。
- 网址检查只在体检时做，任务等收录要等下一次体检（任务盯着的网址每天最多查一次）。
- 体检只看站点首页能链到、或 sitemap 里列出的前 300 页；更大的站会显示「达到 300 页上限」。
- Windows 上没有测过保存对话框。

**已知问题 / 待办**：
- 手工端到端验收（Task 24 Step 2：真实登录、连接负责人自己的 Google 账号、5 个站点同步、和 Search Console 网页对比数字、7d/28d/90d 切换、1440×900 截图和 `design/mockups/2-console.html` 对比）需要负责人的 Google OAuth「桌面应用」客户端和已部署的后端，尚未执行，列为待办。
- 后端改动（`desktop-auth` 分支）尚未部署，需要走 DevOpsPad 发布流程并经负责人同意。
- 后端 MySQL 相关测试本机未跑（Docker 容器起不来，本机无 MySQL）；已完成的只有 `go build` / `go vet` / 编译期测试检查（`go test -run XXX`）。
- Windows 上的传输机制、以及连续触发验证码的流程，都还没有测试环境可以验证。
- Google OAuth 应用的审核（面向更多用户发布前，Google 可能要求验证）还没做。

**如何运行**：

```bash
cd frontend && npm run bindings   # 生成 Wails TS 绑定
SITEMOMENT_DATA_DIR=/tmp/smd-dev \
SITEMOMENT_GOOGLE_CLIENT_ID=<负责人提供> SITEMOMENT_GOOGLE_CLIENT_SECRET=<负责人提供> \
wails3 dev
```

注意：后端必须经过 `/api` 前缀访问（生产环境 `https://sitemoment.com/api`），因为确认页登录会 POST 到 `/api/auth/desktop/approve`；一个没有 nginx 之类反向代理剥离 `/api` 的裸本地后端无法完成登录。

**Bing Webmaster API key**：Bing Webmaster Tools → 右上角设置 → API 访问 → 生成 API key，复制到 SiteMoment 的「设置 → Bing Webmaster」。

**采集器手工检查**（会打开一个隐藏窗口查一次谷歌；遇到验证码会弹出窗口，需要人来处理，程序不会自动处理）：

    SITEMOMENT_DATA_DIR=/tmp/smd-check go run ./cmd/collect-check -c google-serp -q "thumbnail maker"

**自己的模型 key**：设置 → AI 模型，选「OpenAI 兼容接口」（填 base URL，如 `https://api.openai.com/v1`、模型名和 key）或「Anthropic」（填 key，模型默认 `claude-opus-5`），点「测试连接」。不登录 SiteMoment 也能用。

**体检和周报手工检查**（用临时数据目录；example.com 只有一页，几秒就体检完）：

    SITEMOMENT_DATA_DIR=/tmp/smd-m4 go run .
    # 添加站点 example.com → 几秒后站点列表出现健康分 92；报告页有第一份周报；状态栏「通知 1」

## 开发

    go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
    wails3 dev

需要的环境变量（开发时）：

| 变量 | 作用 |
|---|---|
| `SITEMOMENT_API` | SiteMoment 后端地址，默认 `https://sitemoment.com/api` |
| `SITEMOMENT_GOOGLE_CLIENT_ID` / `SITEMOMENT_GOOGLE_CLIENT_SECRET` | Google OAuth「桌面应用」客户端 |
| `SITEMOMENT_DATA_DIR` | 数据目录，默认系统应用数据目录下的 `SiteMoment` |

测试：`go test ./...`
