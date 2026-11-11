# Agent notes

- 每个 Go 源文件第一行是 `// SPDX-License-Identifier: AGPL-3.0-or-later`。
- 业务逻辑放在 `internal/*`，不要依赖 Wails；只有 `main.go`（含 `notify.go`）、`cmd/*` 和 `internal/webview`（采集器窗口）引入 `github.com/wailsapp/wails/v3`。例外：`internal/updates` 只引入纯 Go 的 `pkg/updater`（不开窗口；更新事件只发主窗口，不用 `app.Updater`，它的事件会广播到所有窗口）。
- 采集器窗口里打开的是第三方网页：应用事件只发给主窗口（不要用会广播到所有窗口的 `Event.Emit`），`webview.Middleware` 必须挂在 `AssetOptions.Middleware` 上，挡住采集器窗口访问应用资源和 `/wails/runtime`。
- `OriginInfo.IsMainFrame` 在 Windows/Linux 上 Wails 一直给 false（不是「不是主框架」，是这个字段没填），所以 `internal/webview` 用 `isMainFrame`（按平台 build tag，darwin 用 `IsMainFrame`，其余平台比较 `Origin`/`TopOrigin`），不要直接读 `o.IsMainFrame`。
- 密钥（Google refresh token、SiteMoment refresh token、模型 key）只放 `internal/secret`，不写数据库、不写日志。
- 界面风格以 `design/mockups/2-console.html` 为准；颜色用 `frontend/src/theme.css` 的变量。
- 测试数据不能写进用户的数据目录：测试一律用 `t.TempDir()`；手工运行用 `SITEMOMENT_DATA_DIR=/tmp/...`。
- `main.go` 嵌入 `frontend/dist`，所以 `go build ./...`、`go vet ./...` 之前先 `cd frontend && npm run build`，并 `wails3 generate bindings -ts -i`（和 `build/Taskfile.yml` 一致；也可以 `cd frontend && npm run bindings`）。裸的 `wails3 generate bindings` 只生成带 JSDoc 的 `.js`，没有 `.d.ts`，`api.ts` 对着它类型检查不严。
