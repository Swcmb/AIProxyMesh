# AGENTS.md

本仓库是一个自包含的单一产品仓库：仓库根目录即 Go 模块根，包含完整源码、前端、测试与发布配置，没有任何子模块。

- 根目录：Go 模块（`github.com/Swcmb/AIProxyMesh/mesh`，Go 1.25.5），提供 OpenCode API 网关、统一代理出口池、模型排名与轮转。单二进制、单 Docker 镜像、内嵌 React WebUI。

## 构建与测试（在仓库根目录执行）

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...

# 前端（仅修改 webui/ 或需要刷新内嵌产物时）
cd webui && npm ci && npm run build
# 产物写入 internal/webui/dist 并嵌入 Go 二进制

# WebUI E2E（Chromium + Firefox）
go build -o mesh ./cmd/mesh            # Windows: -o mesh.exe
cd webui && npx playwright install chromium firefox   # 首次
npx playwright test

# 单镜像
docker build -t mesh:local .
```

仓库根目录没有 Makefile；自动化脚本位于 `.github/scripts/` 与 `npm/scripts/`（后者生成并发布 npm 二进制包装包）。

## 架构边界

- **上游地址唯一来源**：`upstream.zen/go/anonymous`；禁止新增 `target_domain` 或第二份目标地址。
- **尝试预算**：唯一总预算 = `retry.max_attempts`；匿名阶段与各 tier 共享，换出口消耗一次。
- **冷却分层**：节点层 `egress.cooldown`（30–900s，`Retry-After` 可延长至 24h）与通道层 `performance.failure_cooldown_seconds`（Key）独立；同一失败只触一层。
- **节点准入与探测**：新节点首次连通信号（主动探测或真实流量）之前不进入路由（`direct` 除外）；路由优先选择近期验证成功的节点，全池未验证时才回退；主动探测使用双优先级固定 worker 池与 13–17s 抖动扫描，失败节点指数退避（上限 30min），批量刷新以 TCP 预检 + 并发探测执行并支持取消；出口 IP 按 `egress.probe.egress_ip_*` 低频探测并优先用于地区解析；`ephemeral: true` 的订阅会驱逐连续熔断超过 `evict_delay_seconds` 的节点。
- **归因纪律**：401/403 与上游全网共识只走通道层；连接失败/单出口 429/可归因 5xx 只走节点层；direct 连接失败视为上游不可达。例外：403 响应体明确为地区限制时按出口国家临时隔离该地区 30 分钟并换出口（不冷却节点，全部地区被隔离时回退）。
- **错误终止**：400/422 的请求级拒绝（上下文超长、参数非法）立即原样返回，不跨出口、Key 或 tier 重试；匿名通道可用 `models.anonymous_backoff` 熔断。
- **单一出口池**：`proxies` / `proxyfile` 经兼容层进入同一节点池；不保留平台管理、请求头规则、粘性租约、反向代理、多接入点模块。
- **模型排名**：排名对象是「模型 + 任务类型」，Zen/Go 作为接入路线合并（费用取最省、可用性与能力取最好、遥测汇总）；综合分 `G × (Q/E/A/F)`，硬性准入先行，模型管理页的列表按此顺序排列。
- **轮转顺序**：`sota` / `sweet` 的模型顺序由排名引擎按 `ranking.sota_task` / `ranking.sweet_task` 自动生成；配置与 API 不再接受手写顺序。
- **认证**：管理端统一使用 session + CSRF；`/api/egress/*` 与 `/api/rankings` 沿用同一认证。
- **配置与状态分离**：业务配置在 `config.json`；节点/订阅/健康/延迟/事件在 `egress.state_path` 的 SQLite 中独立更新；请求与尝试日志在 `logging.requests.state_path` 的独立 SQLite，写入走缓冲，不阻塞请求。
- **许可证**：整体 GPL-3.0-or-later；第三方声明见 `NOTICE`。

## CI 与自动化

- `.github/workflows/checks.yml`、`release.yml`：Go 矩阵、test/race、WebUI 构建（checks 还包含 Chromium + Firefox 的 Playwright E2E）；`v*` 标签发布六平台二进制、GHCR 镜像与 npm 包（`@swcmb/mesh` 主包 + 六平台二进制包，OIDC provenance 或 `NPM_TOKEN`）。
- `.github/workflows/update-dependencies.yml`：每天更新 Go 补丁依赖与前端依赖；有变化时推送 `main` 并显式触发 checks。
- `.github/workflows/upstream-sync.yml` + `.github/scripts/upstream-sync.sh`：每天拉取 opencode2api / Resin，逐文件尝试 `git apply --3way`，干净的改动开草稿 PR，冲突写带 `upstream-sync` 标签的 Issue；状态在 `project-context/upstream-state.json`。
- `.github/workflows/version-bump.yml`：带 `upstream-sync` 标签的 PR 合入后按保守规则升版、打 `v*` 标签，并显式分发 release 与 checks（`GITHUB_TOKEN` 推送不会触发工作流）。
- 失败邮件由 `notify-failure.yml` 处理，依赖仓库 Secrets：`SMTP_SERVER`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`NOTIFY_EMAIL`（可选 `SMTP_PORT`、`SMTP_SECURE`、`SMTP_FROM`）。

## 文档

- 产品说明与构建/部署：`README.md`
- 配置参考：`docs/configuration.md`
- API 参考：`docs/api.md`
- 模型排名规格：`project-context/specs/model-ranking.md`
- 重构记录：`project-context/refactor-report.md`

## 维护规则

当项目结构、构建测试命令、架构边界或开发约定发生变化时，必须在同一次改动中同步更新本文件。
