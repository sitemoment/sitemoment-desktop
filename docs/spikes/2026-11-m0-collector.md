# M0 采集器技术验证（2026-11-10）

代码：`cmd/spike-collector`（提交见本次提交 SHA，建仓库后可用 `git log -- cmd/spike-collector` 查）。Wails v3.1.0。

macOS: `sw_vers` → `ProductName: macOS` `ProductVersion: 27.0.1` `BuildVersion: 26A434`（arm64, Go 1.26.4）。

命令：

```
go run ./cmd/spike-collector -q "thumbnail maker,youtube thumbnail size"
```

跑了一次按 Step 6 范围约定的两查询检查，另外跑了两次针对单一查询、1 分钟内结束的调试探针（替换 `google_serp.js` 为诊断脚本，确认完立即还原，未提交），用来查清为什么 `organic` 是空的。未做「连续 20 次触发验证码」的部分——按负责人范围约定标记为未测（故意跳过）。

## 过程中发现并修的一个问题（偏离计划原文）

计划里 `assets/` 目录只放了 `boot.html`。实测 Wails 的 `AssetFileServerFS`（`internal/assetserver/asset_fileserver.go`）在构造时会在整棵资源树里找 `index.html` 来确定资源根目录；找不到就对**所有**请求返回错误，日志里能看到：

```
7:56AM ERR [AssetFileServerFS] Unable to handle request url=/boot.html err=no `index.html` could be found in your Assets fs.FS
```

这不是「ExecJS 不执行」那类核心机制失败——是资源服务器本身在 `/boot.html` 请求上就失败了，`win:OnWindowEvent` 的运行时就绪事件从未触发。加了一份内容与 `boot.html` 相同的 `cmd/spike-collector/assets/index.html`（纯粹满足 Wails 资源根查找，程序仍然用 `URL: "/boot.html"` 加载引导页）后，资源服务器正常工作：

```
7:59AM INF [AssetFileServerFS] Handling request url=/boot.html file=boot.html
```

这是对计划文件列表的一个必要补充，不改变架构（没有 patch Wails、没有 cgo），只是多了一个文件。

## 逐项核对（macOS 27.0.1, arm64）

| 检查项 | macOS（27.0.1） | Windows |
|---|---|---|
| 引导页发出 runtime ready | 是——日志 `runtime ready from boot page` | 未测（无 Windows 机器） |
| 跳到 Google 后 ExecJS 生效 | 是——两个查询都出现 `navigation finished; injecting run ...` 紧跟 `RESULT after ...` | 未测 |
| 第二次导航后 ExecJS 仍生效 | 是——第二个查询（`youtube thumbnail size`，新 run id `fl_o9fzaii-K`）注入成功并收到 RESULT，证明导航后 ExecJS 不会重新排队 | 未测 |
| 消息到达 RawMessageHandler | 是——`raw: window=collector origin=... len=...` 日志逐条出现，包括一条来自引导页本身（见下方「意外但正确」）被 `Verify` 正确拒绝 | 未测 |
| OriginInfo.Origin 的格式 | 完整网址，不是 `scheme://host`。例：`https://www.google.com/search?q=thumbnail+maker&gl=us&hl=en&num=10`；引导页本身是 `wails://localhost/boot.html` | 未测 |
| 隐藏窗口能正常加载 | 是——未传 `-show`，`Hidden: true`；全程日志里 `captcha: showing the window`（唯一会调用 `win.Show()` 的分支）从未出现，说明窗口没有被显示过；未做屏幕录制，以日志为证据 | 未测 |
| 结果与浏览器一致（前 3 条） | **部分失败**，见下方单独说明 | 未测 |
| 验证码时弹出窗口并能继续 | 未测——按负责人范围约定，故意跳过 20 次连续查询触发验证码的部分 | 未测 |
| 每次耗时 | 查询 1（`thumbnail maker`）：首次 RESULT 在 SetURL 后 1.406s；同一个 run 又收到一次 RESULT（3.091s，见下方说明）。查询 2（`youtube thumbnail size`）：593ms | 未测 |

## 「结果与浏览器一致」—— 实测细节（部分失败，但不是核心机制失败）

用计划 Step 2 给的原始 `google_serp.js`（选择器 `#search a h3`、`#botstuff a[href^='/search']`）跑两查询检查时，两次 RESULT 的 `organic` 和 `related` 都是空数组：

```
RESULT after 1.406s: {"url":"https://www.google.com/search?q=thumbnail+maker&gl=us&hl=en&num=10","organic":[],"related":[],"html":91911}
RESULT after 593ms: {"url":"https://www.google.com/search?q=youtube+thumbnail+size&gl=us&hl=en&num=10","organic":[],"related":[],"html":80642}
```

`html` 字段（91911 / 80642 字节）说明页面本身加载了真实、有内容的 DOM，不是空白或错误页，也没有触发 `need_human`（没有 `/sorry/` 路径，没有 `form#captcha-form`）。为了弄清楚为什么选不到结果，临时把 `google_serp.js` 换成一个诊断脚本（跑完立刻还原，没提交），针对 `thumbnail maker` 单独查询了一次，拿到：

```
{"title":"thumbnail maker - Google Search","hasSearchDiv":false,"h3Count":9,"searchH3Count":0,
 "anyH3Text":["YouTube Thumbnail Maker - Kapwing","Free AI YouTube Thumbnail Maker & Generator - No Sign-Up - Fliki","Free YouTube Thumbnail Maker - VistaCreate","Best Thumbnail Maker Apps & Software (2026)","Create YouTube Thumbnail Online | Free Thumbnail Maker - Pixelixe"],
 "captchaForm":false,"sorryPath":"/search"}
```

结论：页面上确实有 9 个 `h3`，每个都包在 `<a href=...>` 里（看起来就是前 3 条组织结果：Kapwing、Fliki、VistaCreate——标题内容和用无痕浏览器搜同一个词时大致能对上，属于看起来合理的结果，具体一致性请负责人自己开浏览器核对），但 `hasSearchDiv: false`——当前 Google 搜索结果页的 DOM 里已经没有 `id="search"` 这个容器了，所以 `#search a h3` 选不到任何东西，`organic` 永远是空数组。同理 `#botstuff` 也不存在了（第二次诊断探针 `hasBotstuff:false`），相关搜索链接改成了直接的 `a[href^='/search?q=']`（探到 11 个，样例文本 `Images`、`Videos`、`News`、`Books`、`Past hour`——这些其实是搜索工具栏链接，不是「相关搜索」，说明就算改选择器成 `a[href^='/search?q=']` 也会混入非目标链接，需要更细的过滤）。

另外发现：大多数 `h3` 外层 `<a>` 的 `href` 不是直达网址，而是 `https://www.google.com/url?opi=...&q=<一串不透明 token>&sa=U&...`——`q=` 参数不再是可逆的目标网址（只有 1/9 条例外，`q=` 直接是明文的 `play.google.com` 链接）。也就是说，即使选择器修好了，`a.href` 本身也不再能当「真实网址」用——这是现在 Google SERP 的反爬变化，和 M0 要验证的 Wails 传输机制无关，但会影响将来真正采集器的实现方式（需要换一种办法拿到落地页网址，比如看 `cite` 文本或者接受只能拿到标题/摘要不拿精确 URL）。

这个发现不影响 M0 的核心结论：Wails 隐藏窗口 + 引导页 + `ExecJS` 注入 + `RawMessageHandler` 回传的链路本身是通的，第二次导航后注入也没有失效。只是计划里给的采集脚本选择器已经过时，`organic`/`related` 字段需要在真正做采集器时按当前 Google markup 重写并处理 `url?q=` 的 token 化问题。

## 一个「意外但正确」的现象：引导页自己的导航完成事件也会触发一次注入

日志里能看到，每次 `next()` 调用 `win.SetURL(target)` 之后，先出现一次：

```
navigation finished; injecting run Zc6e2OeQMk16
raw: window=collector origin="wails://localhost/boot.html" top="" main=true len=167
rejected: collect: message from a host the collector may not read
```

再出现一次指向真实 Google 网址的注入和 RESULT。看日志时间线，这是因为 `onNavigated` 的回调除了会在跳转到 Google 完成后触发，还会在**引导页自身**的导航完成事件触发（这个事件在 runtime-ready 之后才排上队，和 `SetURL` 几乎同时发生，先后顺序不保证）。此时 `run` 已经被 `next()` 设成了新 run，于是 `ExecJS` 把采集脚本注入到了还没跳转走的 `boot.html` 页面上，脚本在 `boot.html` 的 DOM 上跑了一遍（找不到 `#search`、没有验证码表单），试图 `sm.post("result", ...)`，但 `collect.Verify` 因为 origin 是 `wails://localhost/boot.html`（不在 `run.Hosts=["www.google.com"]` 里）正确拒绝了它。**这正是 `internal/collect.Verify` 设计要防的那类消息**，验证通过，没有把引导页的空结果误当成真实结果。

另外观察到：第一个查询在 `3.091s` 时又收到了一条来自同一个 run（`Zc6e2OeQMk16`）、URL 多了 `&sei=...` 参数的 RESULT。看起来是 Google 在页面加载完之后用前端路由/`history` API 把 URL 换了一次（带上 `sei` 会话参数），这又触发了一次 `WebViewDidFinishNavigation`，`onNavigated` 又把同一个 run 的脚本注入了一次，产生了第二条 RESULT。两条 RESULT 都被接受（因为 run id/nonce 没变，origin 仍是 `www.google.com`），各自都调度了一次 `gap` 之后的 `next()`；第二次 `next()` 调用时 `index` 已经等于 `len(list)`，直接打印 `done` 退出——程序提前结束，但两条查询都已经各自跑完、各自收到过一次有效 RESULT，没有跳过或污染任何一条查询的结果。这是示例程序里 `index`/`run` 状态机的一个小粗糙点（重复导航会让同一个 run 多发一次 RESULT、多排一次 `next()`），不影响 M0 的结论，值得在正式采集器里用「已经收到过这个 run 的 result 就忽略后续同 run 消息」来收紧。

## 结论

- 能否按设计文档 §5 做采集器：**是**——引导页→`wails:runtime:ready`→`SetURL`→导航完成→`ExecJS`→`RawMessageHandler`→`collect.Verify` 这条链路在 macOS 27.0.1 上完整走通，包括连续两次导航后 `ExecJS` 仍然生效。
- 需要改的地方：
  1. **资源文件**：`assets/` 目录必须有 `index.html`（哪怪内容和 `boot.html` 一样），否则 `AssetFileServerFS` 对所有请求都报错，引导页永远加不起来。已在本次提交里加上。
  2. **采集脚本选择器已过时**：当前 Google 结果页没有 `#search`、`#botstuff` 这两个容器 id 了（`hasSearchDiv:false`、`hasBotstuff:false`），计划 Step 2 给的 `google_serp.js` 在真实页面上 `organic`/`related` 永远是空的。正式做采集器时需要用当前页面结构重新找选择器（而且 `h3` 外层 `<a>` 的 `href` 多数是 `/url?q=<不透明 token>` 形式，不能直接当真实落地页网址用）。这是内容/选择器层面的问题，不是 Wails 传输机制的问题；按计划原文的 Step 2 代码照抄进了本次提交（没有擅自改动），实测结果和过时原因都记录在上面。
  3. **重复导航会重发同 run 的 RESULT**：引导页自身的导航完成事件、以及 Google 疑似用 `history` API 换 URL 触发的二次导航，都会让 `onNavigated` 再注入一次同一个 run。前者被 `collect.Verify` 的来源校验正确挡掉；后者没被挡（因为确实是同源同 run），产生了重复 RESULT 和多一次 `gap` 调度。真实采集器应该在收到某个 run 的第一条 `result`/`need_human` 后，标记该 run 已完成并忽略它后续的消息。
  4. Windows 和「连续 20 次触发验证码」两部分完全未测，需要负责人在 Windows 机器上和/或自行决定是否触发验证码来补测。
