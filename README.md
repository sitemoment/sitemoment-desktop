# SiteMoment Desktop

开源的本地 SEO 运营客户端：多个站点的 Search Console、GA4、Bing 数据保存在你自己的电脑上，SEO Agent 在本机分析。

- 许可证：AGPL-3.0-or-later
- 设计文档：sitemoment 仓库 `docs/superpowers/specs/2026-11-10-sitemoment-desktop-design.md`

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
