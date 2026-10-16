# go-plan — opencode2api × Resin 整合

- Spec: `project-context/specs/SPEC-20261016-181006-opencode2api-resin-integration.md`（status: approved）
- Branch: `feature/opencode2api-resin-integration`（自 main@`8ac8a99` 创建）
- Module: `mesh/`（`github.com/Swcmb/AIProxyMesh/mesh`；单模块、单二进制）
- 任务粒度：共 8 个任务，与规格阶段 0–7 一对一（Task N ↔ 阶段 N-1）；全部串行（共享 `mesh/**` 且逐阶段依赖）
- 执行纪律：每任务完成 = 代码审查通过 + 自动化测试通过 + git 提交；证据形式按规格 §7；结果态 DONE / DONE_WITH_CONCERNS / NEEDS_CONTEXT / BLOCKED

## Tasks

Task 1: 阶段 0 — o2a 全量功能盘点（API 路由、三种协议处理、Key 池、模型轮转、配置、管理 API、前端页面、构建流程、现有测试），产出 feature matrix — satisfies: R1 — depends on: none — modifies: `project-context/feature-matrix.md`
Task 2: 阶段 1 — `mesh/` 骨架 + `go.mod`（toolchain go1.25.5）+ 关键决策文档 + o2a 代码树机械迁入（import 路径改名）+ Tier B 依赖审查首轮裁决 — satisfies: R12, R13 — depends on: Task 1 — modifies: `mesh/go.mod`、`mesh/go.sum`、`mesh/cmd/mesh/main.go`、`mesh/internal/**`、`mesh/webui/**`、`project-context/decisions.md`、`project-context/specs/SPEC-20261016-181006-opencode2api-resin-integration.md`（§5.1 三态表裁决回填，仅当裁决变更）
Task 3: 阶段 2 — Tier A 节点基础设施（节点/订阅/去重/健康/延迟/调度/冷却；proxies/proxyfile 兼容） — satisfies: R5 — depends on: Task 2 — modifies: `mesh/internal/egress/**`、`mesh/internal/config/**`、`mesh/internal/admin/**`
Task 4: 阶段 3 — 上游请求接入调度器（单一预算池、候选去重、U_i 归因、429/Retry-After、上游整体故障判断、观测出口） — satisfies: R6, R7, R8, R9 — depends on: Task 3 — modifies: `mesh/internal/gateway/**`、`mesh/internal/egress/routing/**`、`mesh/internal/egress/state/**`、`mesh/internal/telemetry/**`
Task 5: 阶段 4 — egress 配置节 + `/api/egress/*`（统一 session+CSRF 认证）+ 热重载 — satisfies: R10 — depends on: Task 4 — modifies: `mesh/internal/config/**`、`mesh/internal/admin/**`、`mesh/internal/egress/subscription/**`、`mesh/internal/egress/state/**`
Task 6: 阶段 5 — WebUI 13 页重构（Vite+React+TS 构建期嵌入；禁止页面：Platform 管理 / Request Header Rules / Sticky Leases / Resin Endpoints） — satisfies: R11, R12 — depends on: Task 5 — modifies: `mesh/webui/**`、`mesh/internal/webui/**`
Task 7: 阶段 6 — 全量回归 + 故障注入复验（integration verify；对 §9-1~-4 收口） — satisfies: R1, R2, R3, R4 — depends on: Task 6 — modifies: `project-context/regression-report.md`、`project-context/fault-injection-report.md`
Task 8: 阶段 7 — 发布（Docker/跨平台产物、升级回滚演练、文档、许可证与 NOTICE、根 `AGENTS.md` 同步、单实例冒烟） — satisfies: R10, R13 — depends on: Task 7 — modifies: `mesh/Dockerfile`、`mesh/README.md`、`mesh/docs/**`、`mesh/LICENSE`、`mesh/NOTICE`、`AGENTS.md`

## Execution Order

- Batch 1 (sequential): Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7 → Task 8

全部任务串行（共享 `mesh/**` 且逐阶段依赖，无并行批次）。Task 7 承担 skill 要求的 integration verify；Task 8 为发布任务、计划内后置，「final task」语义由其承担。R13 的最终完整判定在 go:integrate 对 §9 全 13 项复核时收口。

### Risks

- Go toolchain 1.25.5 缺失（本机 1.25.4）: 安装 ≥1.25.5，`go.mod` 固定 `toolchain go1.25.5`，离线时 `GOTOOLCHAIN=local` 并记录偏差
- SQLite 受阻（modernc.org/sqlite / golang-migrate 构建或运行失败）: 按规格 §5.9 回退 JSON/JSONL，阶段 1 在 decisions.md 记录
- Tier B 依赖审查不通过: v1 仅 Tier A 协议集（HTTP/HTTPS/SOCKS5/SOCKS5H + proxyfile），Tier 外节点标记 `unsupported`（可见不入池）
- API 兼容性破坏: 阶段 6 全量回归 + 故障注入复验后修复，未恢复前不得进入阶段 7
- 冷却分层 / 单一预算池违例: 单元断言（同一失败只触一层、换节点消耗一次尝试、总预算 ≤ `retry.max_attempts`）+ 故障注入验证
- 前端工作量超预期: 13 页分批交付，但 §9-11 验收标准不降级
- 子模块被误改（上游只读镜像）: 立即触发 §11 停止条件并上报，不提交子模块内改动
- 修复 3 次未果: 按规格 §8 回滚评估（`git revert` / `git reset --hard <最近稳定提交>`）+ 阻塞上报
