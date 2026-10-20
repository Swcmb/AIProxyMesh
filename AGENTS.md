# AGENTS.md

本仓库包含一个整合产品 `mesh/`，以及两个作为参考与基线的上游子模块。

- `mesh/`：**自包含 Go 模块**（`github.com/Swcmb/AIProxyMesh/mesh`，Go 1.25.5），把 opencode2api 的完整网关能力与 Resin 的代理节点订阅、健康评估、出口调度、冷却恢复整合为一个单体服务。单二进制、单 Docker 镜像、内嵌 React WebUI。
- `Resin/`：Go 代理池网关（子模块，分支 `master`，上游 https://github.com/Resinat/Resin）。用于参考节点/订阅/调度设计与 WebUI 视觉基线。
- `opencode2api/`：Go 网关（子模块，分支 `main`，上游 https://github.com/jasonxu114514/opencode2api）。`mesh/` 的业务代码基线。

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
- **单一出口池**：`proxies` / `proxyfile` 经兼容层进入同一节点池；不保留 Resin 的 Platform、请求头规则、粘性租约、反向代理、多接入点模块。
- **认证**：管理端统一使用 opencode2api 的 session + CSRF；新增 `/api/egress/*` 不引入 Resin Bearer 体系。
- **配置与状态分离**：业务配置在 `config.json`；节点/订阅/健康/延迟/事件在 `egress.state_path` 的 SQLite 中独立更新。
- **许可证**：整体 GPL-3.0-or-later；保留 Resin MIT 声明。详见 `mesh/LICENSE`、`mesh/NOTICE`。

## 子模块

- 子模块是**上游 fork 的只读镜像**：修改应在上游仓库进行，本仓库只通过 `git submodule update --remote` 跟进；不要在子模块内提交本地改动后推送。
- 克隆后子模块默认为空，需 `git submodule update --init`。
- 两个项目均把配置文件（`config.json`、`.env` 等）视为敏感内容，不要提交真实密钥。

## 文档

- 产品说明与构建/部署：`mesh/README.md`
- 配置参考：`mesh/docs/configuration.md`
- API 参考：`mesh/docs/api.md`
- 规格与计划：`project-context/specs/`、`project-context/go-plan.md`
- 功能矩阵与回归/故障注入报告：`project-context/feature-matrix.md`、`project-context/regression-report.md`、`project-context/fault-injection-report.md`
- 决策记录：`project-context/decisions.md`
- 上游设计：`Resin/DESIGN.md`、`Resin/doc/`、`opencode2api/docs/`

## 维护规则

当项目结构、构建测试命令、架构边界、子模块版本或开发约定发生变化时，必须在同一次改动中同步更新本文件。
