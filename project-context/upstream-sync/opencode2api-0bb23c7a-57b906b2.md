# 上游同步报告：opencode2api 0bb23c7a..57b906b2

- 上游：`jasonxu114514/opencode2api@main`
- 提交数：11
- 建议版本：minor

## 提交

```text
57b906b fix(proxy): treat any failed probe as a proxy failure, not just timeouts (#111)
05874a5 fix(webui): stop browsers serving a stale embedded UI after upgrade (#112)
4e2edbf fix(upstream): let the runtime own Accept and Content-Length placement (#143)
5c5100c feat(upstream): match the 2.x client's chat body byte for byte (#142)
f9ef569 feat(upstream): follow the OpenCode 2.1 client's version and /inference routes (#141)
ab55803 docs: document explicit reasoning disable in translations (#151)
394acb5 fix(protocol): preserve explicit reasoning disable (#150)
56a789d feat: add Italian WebUI localization
189ef7e docs: repair Italian README and include it in releases
fee0956 fix(webui): address PR #144 review — auto-detect Italian, wire API-key card, polish Italian copy
22d584f feat: add Italian localization for the WebUI
```

## 自动应用（3）

internal/gateway/gateway.go
internal/admin/server.go
internal/gateway/pool.go

## 冲突需人工重写（7）

README.md
webui/index.html
.github/workflows/release.yml (受保护，需人工)
internal/protocol/bridge.go
internal/protocol/request.go
internal/gateway/upstream.go
internal/httpx/client.go

## 缺基线（6）

webui/js/i18n.js (新增文件，需人工)

README.it.md (新增文件，需人工)

README.vi.md (新增文件，需人工)

README.zh-CN.md (新增文件，需人工)

internal/jsonutil/encode.go (新增文件，需人工)

internal/httpx/bun.go (新增文件，需人工)

## 检查清单

- [ ] 冲突文件已按本仓库架构重写
- [ ] 新增第三方文件保留原许可证头并登记到 `NOTICE`
- [ ] `go build ./...`、`go test ./...`、`npm run build` 通过

## 构建验证

- `go build ./...`：失败

```text
go: downloading github.com/oschwald/maxminddb-golang v1.13.1
go: downloading golang.org/x/crypto v0.57.0
go: downloading github.com/golang-migrate/migrate/v4 v4.19.1
go: downloading modernc.org/sqlite v1.45.0
go: downloading golang.org/x/sys v0.48.0
go: downloading modernc.org/libc v1.67.6
go: downloading github.com/dustin/go-humanize v1.0.1
go: downloading golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546
go: downloading modernc.org/mathutil v1.7.1
go: downloading github.com/google/uuid v1.6.0
go: downloading modernc.org/memory v1.11.0
go: downloading github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec
# github.com/Swcmb/AIProxyMesh/mesh/internal/httpx
internal/httpx/client.go:3:1: syntax error: non-declaration statement outside function body
internal/httpx/client.go:15:1: syntax error: unexpected ==, expected }
internal/httpx/client.go:63:1: syntax error: unexpected >>, expected }
```
