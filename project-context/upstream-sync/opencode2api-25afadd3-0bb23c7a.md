# 上游同步报告：opencode2api 25afadd3..0bb23c7a

- 上游：`jasonxu114514/opencode2api@main`
- 提交数：46
- 建议版本：minor

## 提交

```text
0bb23c7 fix(upstream): stop retrying request-level rejections and cap anonymous exits
ddb5c14 Merge pull request #140 from tubimasky/pr-anon-breaker
07ba89a Merge pull request #127 from cuipengcx90/fix/anonymous-exit-cap
07aa453 Merge pull request #132 from cheerc/fix/free-tier-ip-abuse
8cc5e96 feat(gateway): pause the anonymous lane after repeated failures
fa9a255 fix(upstream): stop retrying request-level rejections and ignore placeholder keys
3e83185 fix(upstream): treat the free-tier IP block as terminal for the exit
f0f064f fix: align the v2 free-tier shape with OpenCode 2.0.32
8899749 Merge pull request #130 from byi4/feat/v2-genuine-free-tier-client
c780e68 feat(upstream): mimic the genuine free-tier client on the v2 lane
ea947bc fix(upstream): cap the number of anonymous exits tried per request
d04ff33 feat(upstream): disguise free-model traffic as the Bun runtime
a70e77f Fix audited gateway issues
81ad284 refactor: drop new prefixes, unify v1/v2 lane naming, audit comments
9aa2133 fix(identity): scope derived upstream sessions to the calling server key
cece86b Merge pull request #126 from TiaraBasori/chore/opencode-2.0.32-shape
b0a6584 chore(upstream): sync the /inference lane shape with OpenCode 2.0.32
83d623d fix(webui): prevent duplicate v prefix in version and link to GitHub repo
a9470df fix(upstream): alias the client read tool to "read" on the legacy /zen/v1 lane
2319941 fix(upstream): restore v1.3.7 request shape and stub tools on legacy /zen/v1 lane
8b61b66 Delete internal/gateway/pool_test.go
ee0fb53 fix(proxy): force fresh tunnels for #pool and stop failing over cooling exits
260211c feat(proxy): support rotating proxy pools via #pool marker
6d83806 fix(protocol): skip non-function Responses tools during cross-protocol bridging
a46de6f Merge branch 'pr-89'
d4f1e13 Merge branch 'pr-98'
7bd517e Merge branch 'pr-100'
076451f feat: prefer legacy /zen/v1 anonymous lane with fallback to /inference
6c9be90 fix(webui): make sidebar nav reachable on short viewports and stop overflow on narrow screens
e6797da fix(webui): drop the zero timestamp from the quota catalog line
a0da31e feat(webui): rebuild console with tabbed pages
6648db9 Merge pull request #1 from hanlap0503/fix/rotation-anonymous-2026-10-16
2626f49 fix: update check-models script to save working models and handle empty responses
f99600e fix: simplify rotation manager configuration and add test for model exclusion
b7be5ed fix: route rotation aliases through anonymous lane
d15aec3 Merge branch 'jasonxu114514:main' into main
a6601da fix: report a proxy error when every proxy is down
6150f7d fix(webui): align language flag in toolbar and auto-detect browser language
1e81532 fix(webui): pin login language selector to top-right
c850165 feat(webui): move language selector to far right of toolbar
184f312 feat(webui): borderless flag selector without dropdown arrow
86ea7da feat(webui): show flag-only language selector aligned with toolbar
6833a5d feat(webui): replace language toggle buttons with dropdown selectors
59eac43 feat: add check-models script for model inference testing
68e7c1b fix(webui): restore flag button styles after css revert
0c016a1 feat(webui): add Vietnamese interface with a runtime language switch
```

## 自动应用（45）

mesh/internal/gateway/gateway.go
mesh/internal/gateway/refresh.go
mesh/internal/models/discovery.go
mesh/internal/protocol/request.go
mesh/internal/config/config.go
mesh/internal/config/proxy.go
mesh/internal/config/secrets.go
mesh/internal/gateway/pool.go
mesh/internal/httpx/client.go
mesh/internal/protocol/response.go
mesh/internal/protocol/stream.go
mesh/internal/telemetry/http.go
mesh/internal/gateway/opencode_tools.json
mesh/internal/admin/api_keys.go
mesh/internal/admin/auth.go
mesh/internal/admin/availability.go
mesh/internal/admin/debug.go
mesh/internal/admin/rotation.go
mesh/internal/buildinfo/version.go
mesh/internal/config/json.go
mesh/internal/config/password.go
mesh/internal/config/persistence.go
mesh/internal/config/server_keys.go
mesh/internal/config/tier.go
mesh/internal/gateway/availability.go
mesh/internal/gateway/runtime.go
mesh/internal/httpx/body.go
mesh/internal/httpx/response.go
mesh/internal/jsonutil/decode.go
mesh/internal/jsonutil/value.go
mesh/internal/models/availability.go
mesh/internal/models/cache.go
mesh/internal/models/catalog.go
mesh/internal/models/pricing.go
mesh/internal/protocol/bridge.go
mesh/internal/protocol/collapse.go
mesh/internal/protocol/content.go
mesh/internal/protocol/errors.go
mesh/internal/protocol/protocol.go
mesh/internal/protocol/rotation_stream.go
mesh/internal/protocol/stream_emitter.go
mesh/internal/protocol/stream_parser.go
mesh/internal/protocol/tool_images.go
mesh/internal/telemetry/logging.go
mesh/internal/telemetry/recovery.go

## 冲突需人工重写（17）

.github/workflows/checks.yml (受保护，需人工)
mesh/webui/index.html
mesh/internal/gateway/first_event.go
mesh/internal/gateway/upstream.go
mesh/internal/gateway/health.go
mesh/internal/gateway/rotation.go
mesh/internal/rotation/state.go
mesh/internal/rotation/state_test.go
mesh/internal/gateway/probe.go
mesh/internal/identity/request.go
mesh/internal/admin/model_quotas.go
mesh/internal/admin/server.go
mesh/internal/config/config.go
mesh/internal/config/rotation.go
mesh/internal/gateway/gateway.go
mesh/internal/gateway/refresh.go
mesh/internal/telemetry/metrics.go

## 缺基线（41）

README.md (新增文件，需人工)

README.vi.md (新增文件，需人工)

README.zh-CN.md (新增文件，需人工)

mesh/docs/management.zh-CN.md (新增文件，需人工)

mesh/webui/app.js (新增文件，需人工)

mesh/webui/embed.go (新增文件，需人工)

mesh/webui/i18n.js (新增文件，需人工)

mesh/webui/styles.css (新增文件，需人工)

mesh/scripts/check-models.sh (新增文件，需人工)

mesh/webui/css/base.css (新增文件，需人工)

mesh/webui/css/components.css (新增文件，需人工)

mesh/webui/css/pages.css (新增文件，需人工)

mesh/webui/css/tokens.css (新增文件，需人工)

mesh/webui/js/api.js (新增文件，需人工)

mesh/webui/js/data.js (新增文件，需人工)

mesh/webui/js/logstream.js (新增文件，需人工)

mesh/webui/js/main.js (新增文件，需人工)

mesh/webui/js/pages/access.js (新增文件，需人工)

mesh/webui/js/pages/account.js (新增文件，需人工)

mesh/webui/js/pages/api-keys.js (新增文件，需人工)

mesh/webui/js/pages/config.js (新增文件，需人工)

mesh/webui/js/pages/diagnostics.js (新增文件，需人工)

mesh/webui/js/pages/model-quotas.js (新增文件，需人工)

mesh/webui/js/pages/overview.js (新增文件，需人工)

mesh/webui/js/pages/rotation.js (新增文件，需人工)

mesh/webui/js/store.js (新增文件，需人工)

mesh/webui/js/ui.js (新增文件，需人工)

mesh/internal/config/proxy_test.go (新增文件，需人工)

mesh/webui/js/i18n.js (新增文件，需人工)

mesh/internal/gateway/pool_test.go (新增文件，需人工)

mesh/cmd/opencode2api/main.go (新增文件，需人工)

mesh/internal/identity/message.go (新增文件，需人工)

go.mod (新增文件，需人工)

go.sum (新增文件，需人工)

mesh/internal/httpx/bun.go (新增文件，需人工)

mesh/internal/httpx/bun_dial.go (新增文件，需人工)

mesh/internal/gateway/abuse.go (新增文件，需人工)

mesh/internal/gateway/free_tier_block_test.go (新增文件，需人工)

mesh/internal/config/placeholder.go (新增文件，需人工)

mesh/internal/gateway/free_tier_error_test.go (新增文件，需人工)

config.example.json (新增文件，需人工)

## 检查清单

- [ ] 冲突文件已按本仓库架构重写
- [ ] 新增第三方文件保留原许可证头并登记到 `mesh/NOTICE`
- [ ] `go build ./...`、`go test ./...`、`npm run build` 通过
