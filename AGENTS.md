# AGENTS.md

本仓库是一个自包含的单一产品仓库：`mesh/` 包含完整源码、前端、测试与发布配置，没有任何子模块。

- `mesh/`：Go 模块（`github.com/Swcmb/AIProxyMesh/mesh`，Go 1.25.5），提供 OpenCode API 网关、统一代理出口池、模型排名与轮转。单二进制、单 Docker 镜像、内嵌 React WebUI。

## 构建与测试（均在 `mesh/` 内执行）

```bash
cd mesh
go build ./...
go test ./...
go test -race ./...
go vet ./...

# 前端（仅修改 webui/ 或需要刷新内嵌产物时）
cd webui && npm ci && npm run build
# 产物写入 mesh/internal/webui/dist 并嵌入 Go 二进制

# 单镜像
docker build -t mesh:local .
```

仓库根目录没有 Makefile / 脚本；在根目录运行 `go` 命令无效。

## 架构边界

- **上游地址唯一来源**：`upstream.zen/go/anonymous`；禁止新增 `target_domain` 或第二份目标地址。
- **尝试预算**：唯一总预算 = `retry.max_attempts`；匿名阶段与各 tier 共享，换出口消耗一次。
- **冷却分层**：节点层 `egress.cooldown`（30–900s，`Retry-After` 可延长至 24h）与通道层 `performance.failure_cooldown_seconds`（Key）独立；同一失败只触一层。
- **归因纪律**：401/403 与上游全网共识只走通道层；连接失败/单出口 429/可归因 5xx 只走节点层；direct 连接失败视为上游不可达。
- **单一出口池**：`proxies` / `proxyfile` 经兼容层进入同一节点池；不保留平台管理、请求头规则、粘性租约、反向代理、多接入点模块。
- **模型排名**：排名对象是「提供商 + 模型 + 任务类型」；综合分 `G × (Q/E/A/F)`，硬性准入先行，按任务类型分别排名。
- **轮转顺序**：`sota` / `sweet` 的模型顺序由排名引擎按 `ranking.sota_task` / `ranking.sweet_task` 自动生成；配置与 API 不再接受手写顺序。
- **认证**：管理端统一使用 session + CSRF；`/api/egress/*` 与 `/api/rankings` 沿用同一认证。
- **配置与状态分离**：业务配置在 `config.json`；节点/订阅/健康/延迟/事件在 `egress.state_path` 的 SQLite 中独立更新；请求与尝试日志在 `logging.requests.state_path` 的独立 SQLite，写入走缓冲，不阻塞请求。
- **许可证**：整体 GPL-3.0-or-later；第三方声明见 `mesh/NOTICE`。

## CI 与自动化

- `.github/workflows/checks.yml`、`release.yml`：Go 矩阵、test/race、WebUI 构建；`v*` 标签发布六平台二进制与 GHCR 镜像。
- `.github/workflows/update-dependencies.yml`：每天更新 Go 补丁依赖与前端依赖；有变化时推送 `main` 并显式触发 checks。
- 失败邮件由 `notify-failure.yml` 处理，依赖仓库 Secrets：`SMTP_SERVER`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`NOTIFY_EMAIL`（可选 `SMTP_PORT`、`SMTP_SECURE`、`SMTP_FROM`）。

## 文档

- 产品说明与构建/部署：`mesh/README.md`
- 配置参考：`mesh/docs/configuration.md`
- API 参考：`mesh/docs/api.md`
- 模型排名规格：`project-context/specs/model-ranking.md`
- 重构记录：`project-context/refactor-report.md`

## 维护规则

当项目结构、构建测试命令、架构边界或开发约定发生变化时，必须在同一次改动中同步更新本文件。
