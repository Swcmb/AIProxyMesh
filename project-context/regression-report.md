# 阶段 6 回归报告

| 项 | 值 |
| :--- | :--- |
| 日期 | 2026-10-20 |
| 平台 | Windows 10.0.26100 / amd64 |
| Go | go1.25.5 windows/amd64 |
| Node / npm | v24.20.0 / 11.19.0 |
| 前端 | Vite 7 + React 19 + TypeScript 5.9 |
| 基线 | `mesh/`（单模块，`github.com/Swcmb/AIProxyMesh/mesh`） |
| 证据 | 命令输出、单元/端到端测试、UI 截图（`artifacts/screenshots/`） |

## 1. 命令级验证

| 命令 | 结果 |
| :--- | :--- |
| `cd mesh && go build ./...` | 通过 |
| `cd mesh && go vet ./...` | 通过（无输出） |
| `cd mesh && go test ./... -count=1` | 全部包通过 |
| `cd mesh && go test -race ./... -count=1` | 全部包通过，无数据竞争 |
| `cd mesh/webui && npm ci && npm run build` | 通过，产物写入 `mesh/internal/webui/dist` 并嵌入 |
| `docker build` | 阶段 7 发布时执行（见发布报告） |

## 2. §9 验收逐项证据

| # | 验收项 | 证据 | 状态 |
| :--- | :--- | :--- | :--- |
| 1 | o2a 功能完整性 | `feature-matrix.md` 逐项更新；协议、Key、模型、管理 API、前端均有实现或测试 | ✅（见 §4 缺口） |
| 2 | API 兼容性 | `TestGatewayEndToEndProtocols`：`GET /v1/models`、`/v1/chat/completions`、`/v1/responses`、`/v1/messages`；`TestSystemOnePassesThroughVerbatim`：`/v1/systemone` 字节级透传 | ✅ |
| 3 | 协议转换 | 同一 Chat 上游响应经 Responses / Anthropic 入口转换；多模态 `image_url` 与 `tools` 抵达上游；SSE 流正常返回 | ✅ |
| 4 | Key 与模型路由 | `TestGatewayKeyRotation`：401 后切换到第二个 Key；`TestAnonymousLaneUsesPublicCredential`：`Bearer public`；`TestRotationFailoverUsesEgress`：sota 首个模型 429 → 切换下一模型并经健康出口成功；`TestFreeModelAvailabilityProbeUsesEgress`：免费模型探测经出口池；模型目录来自上游 `/v1/models` | ✅ |
| 5 | 代理资源 | `TestEngineImportDedupProbeSmoke`：legacy `direct` 与远程订阅去重为 1 个节点、双引用；`TestEngineApplyReconcilesSubscriptions`：订阅新增/替换/删除热更新 | ✅ |
| 6 | 节点故障切换 | `TestEgressFailoverToHealthyExit`（故障代理→direct）、`TestEgressAllNodesFailWithinBudget`（全故障不超预算）、`TestAffinityIsStickyButRespectsHealth` | ✅ |
| 7 | 上游故障判断 | `TestEgressUpstreamWideStopsWithoutCoolingNodes`：两个出口同 5xx → 停止、节点不冷却；`TestExitFailureConsensusBecomesUpstreamWide` | ✅ |
| 8 | 429 策略 | `TestEgress429SwitchesExit`：`Retry-After: 2` 出口冷却并切换健康出口；`TestRetryAfterBeyondCooldownCap`：不被 900s 截断；`TestCooldownBackoffAndRetryAfter` | ✅ |
| 9 | 流式响应安全 | SSE 首输出前失败才重试；`TestGatewayEndToEndProtocols` 验证流式输出；`first_event.go` 原语义未改 | ✅ |
| 10 | 配置和状态 | `TestEnginePersistsNodeState`（禁用/冷却/延迟重开恢复）、`TestSaveLoadRoundTrip`、`TestPersistenceAcrossReopen`、`TestEgressSaveRoundTrip`；SQLite WAL + 原子配置保存 | ✅ |
| 11 | WebUI | 13 路由逐页 Chrome 渲染无控制台异常；截图见 `artifacts/screenshots/`；登录、会话、CSRF、配置/订阅/节点/轮转页面可用 | ✅ |
| 12 | 精简范围 | 未引入 Platform / Request Header Rules / Sticky Leases / Resin Endpoints 页面或接口；`grep` 仅命中说明文档 | ✅ |
| 13 | 构建和质量 | `go build` / `go test` / `go test -race` / `go vet` / 前端构建全部通过；Docker 构建在发布阶段执行 | ✅（Docker 待阶段 7） |

## 3. 管理面冒烟（真实进程，端口 18080/18081）

| 操作 | 结果 |
| :--- | :--- |
| `POST /api/auth/login` | 200，返回 CSRF；HttpOnly Session Cookie |
| `GET /api/config`（带会话） | 200 |
| `POST /api/rotation/current`（无 CSRF） | 403 `csrf_failed` |
| 同一请求（带 CSRF + Origin） | 通过 CSRF，进入业务校验（沙箱目录为空 → 400） |
| `POST /api/api-keys` / `PATCH` / `DELETE` | 201 / 200 / 200 |
| `GET /api/egress/nodes` | 200，1 个 direct 节点、可用 1 |
| `POST /api/egress/nodes/{id}/probe`（无 CSRF） | 403 |
| 同一请求（带 CSRF） | 200，记录探测时间与延迟 |
| `POST /api/auth/logout` | 204，随后 `GET /api/config` 返回 401 |
| `PUT /api/config`（完整配置，带 CSRF） | 200；`egress` 节与密钥保留，`ring_size` 变更生效 |
| `POST /api/config/reload` | 200 `{applied:true}` |
| `POST /api/config/reveal` | 200，返回完整 server/zen keys |
| `PUT /api/rotation`（当前顺序） | 200，分组与顺序保持 |
| `PUT /api/account`（同用户名、空新密码） | 200；旧会话立即 401；重新登录成功 |
| 13 个前端路由 | 全部渲染出 `h1`，`errors=0` |

## 4. 覆盖缺口（需要真实凭据或外部网络）

| 项 | 说明 |
| :--- | :--- |
| 真实 OpenCode 上游 | 沙箱无法访问 `opencode.ai`；端到端使用本地假上游，验证协议、重试、SSE 与失败分类 |
| 模型能力目录 | `https://models.opencode.ai/api.json` 不可达；测试通过 `ReplaceWithCapabilities` 注入模型与原生协议 |
| 额度看板 | 需要真实 Go Key；接口路径与空账户响应已验证 |
| 免费模型自动探测 | 探测核心路径已用假上游 + 出口池验证；周期调度器未单独做在线 E2E |
| 轮转管理接口 | 轮转故障切换状态机已由 `TestRotationFailoverUsesEgress` 覆盖；管理端“手动切换”在沙箱因无可用模型目录返回 400，接口与 CSRF 已到达业务校验层 |
| Playground 实际推理 | 与真实上游相同，依赖模型目录与 Key；请求构造、诊断标记与不冷却语义由代码与单测覆盖 |
| Docker / 跨平台产物 | 阶段 7 发布任务执行 |

## 5. 结论

在可离线复现的范围内，§9 的 13 项均有代码与测试证据；协议、Key 轮换、匿名通道、出口切换、429、上游共识、预算上限、持久化与 WebUI 13 页均通过。v0.1.1 之后又修复了轮转别名请求绕过出口池的问题：轮转、模型目录刷新与可用性探测现在都使用统一出口池，轮转请求同样受 `retry.max_attempts` 总预算约束。剩余缺口集中在需要真实 OpenCode 凭据/网络的在线行为，发布阶段需用真实配置完成一次冒烟。

UI 证据：

- `artifacts/screenshots/overview.png`
- `artifacts/screenshots/nodes.png`
- `artifacts/screenshots/subscriptions.png`
- `artifacts/screenshots/rotation.png`
- `artifacts/screenshots/config.png`
