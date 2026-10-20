# opencode2api 功能矩阵（阶段 0 盘点）

> 用途：规格 §9-1「opencode2api 功能完整性」的验收依据。逐项记录现有功能、证据位置、整合后验证方式与状态。
> 本文件随阶段 2–7 推进逐项更新状态；任何项标 ❌ 即为回归，阻止阶段验收。
> 基线：opencode2api @ `a6601da`（79 文件；`internal/` 11 包 + `cmd/opencode2api/main.go` + `webui/` 4 文件 + 构建发布 8 文件）。

## 状态图例

| 标记 | 含义 |
| :--- | :--- |
| ⬜ | 未验证（整合尚未触及） |
| 🔄 | 验证中（本阶段正在覆盖） |
| ✅ | 通过（附验证证据） |
| ❌ | 失败/回归（阻止验收） |
| ⚠️ | 有偏差但已记录处置 |

## A. 网关协议与推理路径

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| A1 | `GET /v1/models` 模型列表 | `internal/gateway/gateway.go:102` | 存在，`authenticate` 包裹 | 阶段 7 E2E：原路径请求语义不变 | ✅ |
| A2 | `POST /v1/chat/completions`（OpenAI Chat 协议） | `gateway.go:103`；`internal/protocol/` | 存在，含 JSON/SSE 两态 | 阶段 7 E2E 回归 | ✅ |
| A3 | `POST /v1/responses`（OpenAI Responses 协议） | `gateway.go:104` | 存在 | 阶段 7 E2E 回归 | ✅ |
| A4 | `POST /v1/messages`（Anthropic Messages 协议） | `gateway.go:105` | 存在 | 阶段 7 E2E 回归 | ✅ |
| A5 | `POST /v1/systemone`（SystemOne 结构化决策端点） | `gateway.go:106`；`handleSystemOne` | 存在，原样透传；不在轮转/模型探测范围 | 阶段 4：确认透传路径不受出口调度影响；不纳入轮转 | ✅ |
| A6 | `GET /healthz` | `gateway.go:107` | 存在，无认证 | 阶段 7 回归 | ✅ |
| A7 | 协议转换：Chat/Responses/Anthropic 三向桥接 | `internal/protocol/bridge.go`、`request.go`、`response.go`、`collapse.go`、`content.go`、`errors.go` | 存在 | 阶段 3 单元回归 + 阶段 7 E2E | ✅ |
| A8 | SSE 流式：解析/发射/首事件语义 | `internal/protocol/stream.go`、`stream_parser.go`、`stream_emitter.go`；`gateway/first_event.go` | 存在；首字节超时默认 0（关闭）；重试仅限首字节前；首输出后不可重放 | 阶段 5：以现有行为为唯一基准固定语义（含换节点仅限首字节前） | ✅ |
| A9 | 多模态内容与工具图片 | `internal/protocol/tool_images.go`、`content.go` | 存在 | 阶段 3 回归 | ✅ |
| A10 | 工具调用转换 | `internal/protocol/` 全套；`gateway/opencode_tools.json` | 存在；12 工具定义（edit/execute/glob/grep/question/read/shell/skill/subagent/webfetch/websearch/write） | 阶段 3 回归；匿名 lane 依赖其与上游字节级一致（见 J2） | ✅ |
| A11 | 推理内容（reasoning）字段 | `internal/protocol/content.go`、`response.go` | 存在 | 阶段 3 回归 | ✅ |

## B. 认证与 Key 管理

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| B1 | 本地认证 Key 池（`server_keys`） | `internal/config/server_keys.go`；`internal/admin/auth.go` | 存在；网关侧 `authenticate` 校验 | 阶段 4：Key 池迁入统一存储后语义不变 | ✅ |
| B2 | 上游 Zen Key 池（`zen_keys`） | `internal/gateway/pool.go`、`runtime.go` | 存在；池化 + 故障冷却 | 阶段 4 回归 | ✅ |
| B3 | 上游 Go Key 池（`go_keys`） | 同上 | 存在；`prefer` 默认 `go` | 阶段 4 回归 | ✅ |
| B4 | 匿名通道（anonymous lane） | `gateway/pool.go`；`httpx/client.go:13` | 存在；依赖 UA 与工具定义字节级一致（J2/J3） | 阶段 4：通道身份串完整保留；阶段 7 E2E | ✅ |
| B5 | 管理会话认证（session cookie） | `internal/admin/auth.go`；cookie `opencode2api_session` | 存在；TTL 720 分钟 | 阶段 6：新接口统一沿用此认证（禁 Resin Bearer） | ✅ |
| B6 | CSRF 保护（全部写操作） | `admin/server.go:50-71` | 存在 | 阶段 6：新 `/api/egress/*` 写入同样走 CSRF | ✅ |
| B7 | 登录限流（5 次/5 分钟） | `internal/admin/auth.go` | 存在 | 阶段 6 回归 | ⬜ |
| B8 | API Key CRUD（管理端签发） | `admin/api_keys.go`；`server.go:65-68` | 存在（GET/POST/PATCH/DELETE） | 阶段 6 回归 | ✅ |

## C. 模型发现、可用性与配额

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| C1 | 模型目录发现与缓存 | `internal/models/catalog.go`、`discovery.go`、`cache.go` | 存在 | 阶段 4 回归 | ✅ |
| C2 | 模型可用性追踪（探测） | `internal/models/availability.go`；`gateway/probe.go`、`availability.go` | 存在 | 阶段 4：探测路径经新出口池后语义不变；SystemOne/A5 排除 | ⬜ |
| C3 | 模型配额视图 | `internal/models/pricing.go`；`admin/model_quotas.go` | 存在 | 阶段 6/7 回归 | ⚠️ |
| C4 | 模型手动恢复/手动登记 | `admin/availability.go`；`server.go:58-60` | 存在 | 阶段 6 回归 | ⬜ |

## D. 轮转（rotation）

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| D1 | 轮转状态机 | `internal/rotation/state.go`（Manager） | 存在 | 阶段 4：迁入统一存储，状态语义不变 | ⬜ |
| D2 | 轮转事件流（提交前缓存 ≤256 事件） | `internal/protocol/rotation_stream.go` | 存在 | 阶段 5 回归（SSE 提交语义） | ⬜ |
| D3 | 轮转配置读写与当前项选择 | `config/rotation.go`；`admin/rotation.go`；`server.go:61-63` | 存在（GET/PUT/POST current） | 阶段 6 回归 | ⚠️ |

## E. 管理 API（24 条路由全表）

基线：`internal/admin/server.go:48-72`。1 公开（login）+ 23 session 认证；写操作全部叠 CSRF。

| # | 路由 | 认证 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- |
| E1 | `POST /api/auth/login` | 公开 | 阶段 6/7 回归 | ✅ |
| E2 | `GET /api/auth/session` | session | 阶段 6/7 回归 | ✅ |
| E3 | `POST /api/auth/logout` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E4 | `GET /api/config` | session | 阶段 6：新增字段后读取兼容 | ✅ |
| E5 | `PUT /api/config` | session+CSRF | 阶段 6：旧配置写入（proxies/proxyfile）不失效 | ✅ |
| E6 | `POST /api/config/reload` | session+CSRF | 阶段 6：热重载含新出口配置 | ✅ |
| E7 | `POST /api/config/reveal` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E8 | `PUT /api/account` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E9 | `GET /api/monitor` | session | 阶段 6：监控数据并入新视图 | ✅ |
| E10 | `GET /api/debug/models` | session | 阶段 6/7 回归 | ✅ |
| E11 | `GET /api/models/availability` | session | 阶段 6/7 回归 | ✅ |
| E12 | `POST /api/models/restore` | session+CSRF | 阶段 6/7 回归 | ⬜ |
| E13 | `POST /api/models/manual` | session+CSRF | 阶段 6/7 回归 | ⬜ |
| E14 | `GET /api/rotation` | session | 阶段 6/7 回归 | ✅ |
| E15 | `PUT /api/rotation` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E16 | `POST /api/rotation/current` | session+CSRF | 阶段 6/7 回归 | ⚠️ |
| E17 | `GET /api/model-quotas` | session | 阶段 6/7 回归 | ✅ |
| E18 | `GET /api/api-keys` | session | 阶段 6/7 回归 | ✅ |
| E19 | `POST /api/api-keys` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E20 | `PATCH /api/api-keys/{id}` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E21 | `DELETE /api/api-keys/{id}` | session+CSRF | 阶段 6/7 回归 | ✅ |
| E22 | `POST /api/debug/inference` | session+CSRF | 阶段 6/7 回归 | ⬜ |
| E23 | `GET /api/logs` | session | 阶段 6/7 回归 | ✅ |
| E24 | `GET /api/logs/stream` | session（SSE） | 阶段 6/7 回归 | ✅ |

新增（§5.8，非回归项）：`/api/egress/*` 接口组，沿用 session+CSRF，禁 Resin `/api/v1` Bearer。

## F. WebUI（13 页，`webui/index.html`）

| # | 页（data-page） | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- |
| F1 | `availability` 模型可用性 | 存在 | 阶段 6：Vite+React+TS 重构后保留 | ✅ |
| F2 | `overview` 运行桌面 | 存在 | 同上 | ✅ |
| F3 | `guide` 首次运行 | 存在 | 同上 | ⚠️ |
| F4 | `access` 接入手册 | 存在 | 同上 | ⚠️ |
| F5 | `usage` Token 用量 | 存在 | 同上 | ✅ |
| F6 | `playground` 调试台 | 存在 | 同上 | ✅ |
| F7 | `diagnostics` 诊断 | 存在 | 同上 | ✅ |
| F8 | `config` 配置中心 | 存在 | 同上 | ✅ |
| F9 | `logs` 事件日志 | 存在 | 同上 | ✅ |
| F10 | `account` 账号安全 | 存在 | 同上 | ✅ |
| F11 | `model-quotas` 模型配额 | 存在 | 同上 | ✅ |
| F12 | `api-keys` Key 管理 | 存在 | 同上 | ✅ |
| F13 | `rotation` 模型轮转 | 存在 | 同上 | ✅ |

新增（§9-11）：出口/代理池相关页面（Resin 风格）。禁止模块：Platform 管理 / Request Header Rules / Sticky Leases / Resin Endpoints（§9-12）。

## G. 配置系统

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| G1 | 严格未知字段拒绝 | `internal/config/json.go`（`DisallowUnknownFields`） | 存在 | 阶段 6：新增出口配置字段必须进 schema，旧文件不破坏 | ✅ |
| G2 | 默认值：listen `127.0.0.1:8080` / webui `0.0.0.0:8081` | `config.go:139,145` | 存在 | 阶段 6/7 回归 | ✅ |
| G3 | 默认值：`retry.max_attempts=3`、`timeout_seconds=300` | `config.go:141` | 存在 | 阶段 5：单一预算池以 max_attempts 为唯一总预算 | ✅ |
| G4 | 默认值：`failure_cooldown_seconds=15` | `config.go:143` | 存在 | 阶段 4：通道层冷却保留（与节点冷却两层独立） | ✅ |
| G5 | 代理配置：`direct` + http/https/socks5/socks5h | `config.go:244,252`；`config/proxy.go` | 存在；`http.ProxyURL` 原生处理 | 阶段 4：旧 proxies/proxyfile 进入统一节点池不失效 | ✅ |
| G6 | 持久化与密钥处理 | `config/persistence.go`、`secrets.go`、`password.go` | 存在 | 阶段 1：迁入 SQLite（§5.9），JSON 仅导入导出 | ✅ |
| G7 | Tier 逻辑 | `config/tier.go` | 存在 | 阶段 4：与出口调度解耦，语义不回归 | ✅ |
| G8 | 环境变量：仅 `CONFIG_PATH`/`CONFIG_SEED_PATH` | `cmd/opencode2api/main.go` | 存在；无 `.env` 加载 | 阶段 1/6：保持不变 | ✅ |
| G9 | 配置热重载 | `config.go` reload 路径；`admin/server.go:53` | 存在 | 阶段 6 回归（含新出口配置） | ✅ |

## H. 基础设施与运行时

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| H1 | HTTP 客户端与 UA（`opencode/latest/{ver}/cli`） | `internal/httpx/client.go:13`、`body.go`、`response.go` | 存在；缺失/形状错误 → 上游 403 FreeTierError | 阶段 4：UA 串保留；阶段 7 E2E | ✅ |
| H2 | 遥测（纯内存，无落盘） | `internal/telemetry/metrics.go`、`http.go`、`logging.go`、`recovery.go` | 存在 | 阶段 6/7：并入统一视图不回归 | ✅ |
| H3 | 版本信息（buildinfo） | `internal/buildinfo/version.go` | 存在 | 阶段 7 回归 | ✅ |
| H4 | 请求身份识别（identity） | `internal/identity/request.go` | 存在 | 阶段 4：通道识别逻辑保留 | ✅ |
| H5 | JSON 工具库（宽松解码/值处理） | `internal/jsonutil/decode.go`、`value.go` | 存在 | 阶段 3 回归 | ✅ |
| H6 | 非可重试判定（4xx 非 401/403/429） | `internal/gateway/upstream.go`（`isNonRetryableClientResponse`） | 存在 | 阶段 5：错误分类保留；429 归因三分不误判 | ✅ |
| H7 | 刷新与运行时（refresh/runtime） | `internal/gateway/refresh.go`、`runtime.go` | 存在 | 阶段 4 回归 | ✅ |

## I. 构建与发布

| # | 功能项 | 证据位置 | 阶段 0 现状 | 整合后验证方式 | 状态 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| I1 | Dockerfile（多阶段） | `Dockerfile`、`docker-entrypoint.sh`、`.dockerignore` | 存在 | 阶段 1/7：单二进制 + 单 Docker（§4）；React 构建期嵌入 | ✅ |
| I2 | compose | `compose.yaml` | 存在 | 阶段 7：与新镜像一致 | ✅ |
| I3 | CI：checks + release | `.github/workflows/checks.yml`、`release.yml` | 存在；**无 `go test` 步骤** | 阶段 0/1：补测试骨架；CI 增 `go test ./... -race` + `go vet`（§9-13） | ⚠️ |
| I4 | Go 版本与依赖 | `go.mod`（`module opencode2api`，go 1.24，直接依赖仅 `golang.org/x/crypto v0.41.0`） | 存在 | 阶段 1：迁为 `mesh/` 模块（toolchain go1.25.5） | ✅ |
| I5 | 文档 | `README.md`、`README.zh-CN.md`、`docs/management.zh-CN.md` | 存在 | 阶段 7：更新为整合后形态 | ✅ |
| I6 | 许可证 | `LICENSE`（GPL-3.0） | 存在 | 阶段 1：衍生分发 GPL-3.0-or-later + 保留 Resin MIT 声明（§12 收口） | ✅ |

## J. 盘点注记（后续阶段防回归约束）

| # | 注记 | 含义 |
| :--- | :--- | :--- |
| J1 | SystemOne 为第四协议 | ✅ |
| J2 | 匿名 lane 双约束 | ✅ |
| J3 | 测试基础设施为零 | ✅ |
| J4 | 前端无构建步骤 | ✅ |
| J5 | 事件日志流为 SSE | ✅ |
| J6 | 首字节超时默认 0 | ✅ |
| J7 | 冷却两层独立 | ✅ |

---

盘点基线：`ab12d44`（go-plan 提交）之后、阶段 1 开始之前。核验方式：全量 `git ls-files`（79 文件）+ 路由注册点 grep（gateway 6 条 / admin 24 条）+ 13 页 `data-page` 清单 + 默认值与 scheme 白名单逐行复核 + `*_test.go` 全库检索（0 命中）+ `opencode_tools.json` 顶层键计数（12）。


---

## 阶段 6 回归结论（2026-10-20）

- ✅：由本阶段端到端/管理面冒烟直接覆盖；⚠️：接口存在且可调用，但沙箱缺少真实凭据/模型目录，仅验证到校验层；⬜：本阶段未在线验证，留待真实凭据环境。
- 证据与缺口：`project-context/regression-report.md`、`project-context/fault-injection-report.md`。
- 前端等价关系：旧 `guide`/`access` 信息页折叠进总览与配置页；`account` 并入配置页；其余页面一一对应规格 §5.10 的 13 页。
- Docker、跨平台产物、文档与许可证属于阶段 7，未在本表标记。

阶段 7 发布补充：I1 Dockerfile / I2 compose / I5 README+docs / I6 LICENSE+NOTICE 已完成；I3 工作流已落盘但沙箱未实际触发 CI 运行。
阶段 7 追加：E5/E6/E7/E8/E15/G9 已用真实进程 + CSRF 完成写接口回归（配置保存保留 egress、热重载、密钥揭示、账号更新、轮转顺序保存）。
