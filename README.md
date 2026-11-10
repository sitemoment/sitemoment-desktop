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
