# Agent notes

- 每个 Go 源文件第一行是 `// SPDX-License-Identifier: AGPL-3.0-or-later`。
- 业务逻辑放在 `internal/*`，不要依赖 Wails；只有 `main.go` 和 `cmd/*` 引入 `github.com/wailsapp/wails/v3`。
- 密钥（Google refresh token、SiteMoment refresh token、模型 key）只放 `internal/secret`，不写数据库、不写日志。
- 界面风格以 `design/mockups/2-console.html` 为准；颜色用 `frontend/src/theme.css` 的变量。
- 测试数据不能写进用户的数据目录：测试一律用 `t.TempDir()`；手工运行用 `SITEMOMENT_DATA_DIR=/tmp/...`。
- `main.go` 嵌入 `frontend/dist`，所以 `go build ./...`、`go vet ./...` 之前先 `cd frontend && npm run build`，并 `wails3 generate bindings`。
