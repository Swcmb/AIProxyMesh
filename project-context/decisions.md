# 阶段 1 关键决策记录（decisions）

> 对应 go-plan Task 2 / 规格 §6 阶段 1。状态：**已裁决并落盘（2026-10-20）**。
> 所有决策在后续阶段如需变更，必须在本文件追加新决策（D-xx），不得静默改写既有结论。

## 0. 证据基线

| 项 | 值 |
| :--- | :--- |
| o2a 基线 | `opencode2api` @ `a6601da673c03832bf78ce04b100dee8c8a92385` |
| Resin 基线 | `Resin` @ `1a6954e71b0f25058d14f18d31a7a82276243f2f` |
| 新模块 | `mesh/`，`module github.com/Swcmb/AIProxyMesh/mesh`，`go 1.25.5` |
| 迁移方式 | o2a 全量机械迁入（57 个 Go 文件一一对应；`cmd/opencode2api/main.go` → `cmd/mesh/main.go`，`webui/embed.go` → `internal/webui/embed.go`，import 前缀统一改写） |
| 阶段 1 验证 | `cd mesh && go build ./...`、`go test ./...`、`go vet ./...` 全部通过（2026-10-20；后两者 o2a 基线本无测试文件，见 D-11） |

---

## D-01 模块形态：单模块、单二进制

- **决策**：整合代码全部落在 `mesh/`；单一 Go module、单一可执行文件（`cmd/mesh`）；不引入第二个运行时进程。
- **理由**：Go `internal` 包不可跨模块导入，Resin/o2a 子模块又只读（规格 §5.1、§3.4）；单模块是代码级整合的最小可行形态。
- **影响**：后续所有包（含新增 `internal/egress/**`）都在同一 module 内；子模块仅作上游参考与版本跟进。

## D-02 迁移策略：o2a 全量保留，Resin 按三态表裁剪

- **决策**：o2a 代码树原样迁入（行为基准），仅改包导入前缀与入口路径；Resin 仅迁移规格 §5.1「20 包三态表」中保留/改造的包，未列出一律裁剪。
- **首轮落地映射**（阶段 2–3 执行）：
  - `node` → `internal/egress/node`（裁剪平台/租约字段）
  - `subscription` → `internal/egress/subscription`
  - `outbound` → `internal/egress/outbound`（v1 仅 Tier A + `direct`）
  - `probe` → `internal/egress/probe`
  - `routing` → `internal/egress/routing`（替换为 §5.4 评分）
  - `state` → `internal/egress/state`（SQLite，见 D-06）
  - `netutil` → `internal/egress/netutil`（按实际依赖裁剪）
- **不迁移**：`platform`、`proxy`、`api`、`model`、`requestlog`、`metrics`、`geoip`、`scanloop`、`service`、`topology`、Resin `config`/`buildinfo`（规格 §5.1）。
- **测试**：Resin 被保留包的测试随包迁移并适配；被裁剪包测试不迁移。

## D-03 代理池对网关暴露的最小接口

- **决策**：网关层只依赖一个出口选择抽象，不接触节点内部结构。拟定接口（阶段 3/4 可细化命名，语义冻结）：

```go
// Lease = 一次请求的出口选择；Close 时回写结果。
type Lease interface {
    ProxyURL() *url.URL   // nil = 直连
    NodeID() string       // 空 = 直连
    Report(outcome Outcome) // 成功/失败 + 可归因信号
    Close()
}

type Selector interface {
    Select(ctx context.Context, target *url.URL, exclude map[string]struct{}) (Lease, error)
}
```

- **约束**：
  - `target` 来自 o2a 现有 `upstream.zen/go/anonymous`，调度器不持有独立目标域名配置（规格 §4-3）。
  - `exclude` 承载"同轮不重复节点"（规格 §5.4）。
  - 出口选择不参与 Key 选择；Key 轮换仍在网关层（D-04）。
  - 无合格节点时返回错误，由网关决定回退直连或返回错误（默认：`direct` 始终作为兜底候选，与 o2a 现状一致）。
- **兼容**：`proxies`/`proxyfile` 由兼容层转换为节点池内的直连/HTTP/HTTPS/SOCKS5/SOCKS5H 节点（D-05）。

## D-04 错误分类与冷却分层

- **决策**：完全采用规格 §5.5 四类分类与两层冷却，不新增第三层。
  - 出口相关（连接层故障、仅某出口 429、某出口特有 5xx）→ 节点层冷却（`egress.cooldown`，30–900s，指数退避）。
  - Key/模型故障（401/403、额度、模型下架）与上游整体故障 → o2a 通道层冷却（`failure_cooldown_seconds`，默认 15s）。
  - 同一失败事件只更新一层；判定顺序固定为：先归因 → 出口相关则节点层，否则通道层。
- **归因纪律**：`U_i` 只统计可归因于出口的失败；`401/403` 默认不惩罚代理；客户端取消不计失败；不可重试 4xx 保留原错误语义。
- **上游整体故障**：需多出口共识（不同节点对同一上游相同错误）+ 观测窗口，不以单条 5xx 认定；命中后进入通道层退避，停止遍历节点池。

## D-05 配置迁移规则

- **决策**：
  1. o2a 配置结构、字段名、默认值与 `DisallowUnknownFields` 语义全部保留。
  2. 新增顶层 `egress` 节（规格 §5.8），未设置字段因 omitempty 不出现在保存结果。
  3. `proxies` 与 `proxyfile` 继续作为"旧代理来源"被解析：启动/热重载时转换为节点池条目（来源标记 `legacy:config`），不要求用户改配置；`direct` 保留为兜底出口。
  4. 不新增 `target_domain` 或第二份上游地址；探测目标从 `upstream.*` 派生（规格 §12-⑦）。
  5. 配置校验失败 → 拒绝保存/重载，保持旧运行实例（沿用 o2a 现有原子切换流程）。
- **状态分离**：节点运行状态只写状态库，配置保存不重写状态；订阅刷新不触发配置重写（规格 §4-5）。

## D-06 状态存储：SQLite（维持规格定案）

- **决策**：`modernc.org/sqlite v1.45.0`（纯 Go、无 CGO）+ `github.com/golang-migrate/migrate/v4 v4.19.1` 迁移脚本；节点/订阅/健康/延迟/切换历史入库；JSON/JSONL 仅作导出导入。
- **理由**：Resin 现行实现已验证该组合；纯 Go 驱动避免 CGO 跨平台问题；配置与状态分库、独立恢复。
- **回退条款**：仅当阶段 2/3 出现跨平台构建失败或迁移机制不可行时，按规格 §5.9 回退 JSON/JSONL 状态文件，接口不变并记录本文件。
- **验证计划**：Task 3 引入依赖后执行 `go build ./...` + Windows/Linux（交叉编译）构建；Docker 构建在 Task 8 复验。

## D-07 Tier B 依赖审查：首轮裁决 = v1 不引入

- **决策**：v1 出口协议基线锁定 Tier A（HTTP / HTTPS / SOCKS5 / SOCKS5H + `proxyfile` + `direct`），**不引入** `sing`、`sing-box`、`sing-shadowsocks`、`sing-vmess` 等依赖；Tier 外节点解析入库后标记 `unsupported`（可见、不入候选池）。
- **审查结论（首轮，2026-10-20）**：

| 审查项 | 结论 |
| :--- | :--- |
| 依赖体积 | Resin `go.mod` 显示 sing-box 直接依赖引入约 150 个间接模块（含 gvisor、tailscale、quic-go、wireguard 等），与"单二进制精简"目标冲突 |
| CGO / 跨平台 | 纯 Go 但含平台特定包（wintun、netlink、nftables 等）；Windows/Linux/macOS × amd64/arm64 全矩阵风险高 |
| 验收影响 | 规格 §5.3 明确 Tier B 不计入 v1 验收；纳入只会推迟核心调度能力交付 |
| 许可证 | 依赖树复杂，第三方许可证清单成本高（Task 8 需要逐项收口） |
| 后续路径 | 出站接口保持可扩展；Tier B 作为独立迭代项，届时以最小子集 + `go mod why` 重新审查 |

- **Tier A 零新依赖**已满足（o2a 现有 scheme 集，标准库实现）。

## D-08 许可证策略

- **决策**：整合产物整体按 **GPL-3.0-or-later** 分发（o2a 为 GPL-3.0-or-later，衍生组合作品）；保留 Resin 的 MIT 版权与许可证声明；随附第三方依赖清单。
- **落地**：Task 8 写入 `mesh/LICENSE`（GPL-3.0 全文）、`mesh/NOTICE`（Resin MIT 声明 + 依赖清单）、README `## License` 段；源码文件按仓库既有习惯不逐文件加 SPDX（o2a 基线未加），仅在包级/发布物声明。
- **风险**：GPL 义务无法履行时按规格 §11-2 停止发布并上报。

## D-09 Go 工具链

- **决策**：`mesh/go.mod` 声明 `go 1.25.5`（与 Resin 一致）；本机已安装 `go1.25.5 windows/amd64`，`GOTOOLCHAIN=auto` 保留自动下载作为兜底。
- **可复现性**：CI/本地命令统一 `cd mesh && go build ./...`；离线场景使用 `GOTOOLCHAIN=local` 并记录版本。

## D-10 WebUI 技术栈

- **决策**：阶段 5 按规格 §5.10 使用 Vite + React + TypeScript，构建产物嵌入 `internal/webui`；当前迁入的 o2a 原生 `app.js/index.html/styles.css` 作为**行为与接口基准**保留到重构完成，避免中途失去可用的管理面。
- **约束**：13 页功能清单以 `feature-matrix.md` F1–F13 为准；禁止页面（Platform / Request Header Rules / Sticky Leases / Resin Endpoints）不得出现。

## D-11 测试基础设施

- **事实**：o2a 基线 0 个 `*_test.go`，CI 无 `go test`（feature-matrix J3/I3）。
- **决策**：不自欺地宣称"测试通过"等于有覆盖。阶段 2 起为新增 egress 包补单元测试；阶段 3 补调度/故障注入测试；阶段 6 做全量回归与 E2E。CI 在 Task 8 前增加 `go test ./... -race` 与 `go vet ./...`。
- **阶段 1 证据**：`go test ./...` 输出 `[no test files]`，记录为基线事实，不作为质量证明。

---

## 变更记录

| 时间 | 变更 |
| :--- | :--- |
| 2026-10-20 | 初版：D-01 ~ D-11 落盘（阶段 1 完成） |

---

## 阶段 2–4 实施记录（2026-10-20）

- **D-12 出口池实现**：`internal/egress/{node,parser,pool,outbound,routing,probe,subscription,state}` + `engine`；Tier A 出站与解析零新依赖；Tier 外协议入库标记 `unsupported`。
- **D-13 状态库**：`modernc.org/sqlite v1.45.0` + `golang-migrate v4.19.1`（嵌入迁移 `000001_init`），已在本机 Windows 完成建库/写入/重载测试；表覆盖节点、订阅引用、延迟、上游归因计数与切换事件。迁移驱动要求 `golang.org/x/crypto v0.45.0`（高于 o2a 基线 v0.41.0），已回钉 `go 1.25.5` 并用该版本构建通过。
- **D-14 亲和选择**：网关以 Key 指纹（匿名通道用 Session）做 rendezvous 亲和；亲和节点仍须通过资格过滤，且得分不得低于最佳候选 50%，否则回退加权选择。该机制替代 o2a 的 key→proxy 静态绑定，不依赖租约。
- **D-15 单一预算池落地**：匿名阶段与每个 tier 循环共享 `retry.max_attempts`；tier 在预算耗尽时停止，保留上一 tier 的响应而不是用“预算耗尽”覆盖。换出口消耗一次尝试，同轮通过 exclude 集合禁止重复节点。
- **D-16 归因与冷却落地**：连接失败（非 direct）→ 节点层；direct 连接失败视为上游不可达，不冷却节点；401/403 → 通道层（Key 冷却）；单出口 429/5xx → 节点层；多出口同 Key 同状态类且窗口内无成功 → 上游共识，停止遍历并只触发通道层。`Retry-After` 直接决定节点冷却（上限 24h），不再被 `cooldown.max_seconds` 截短；仅当请求剩余时限可容纳等待时才等待。
- **D-17 可观测性**：遥测与持久化事件均记录出口节点 ID/名称、状态码、尝试序号与切换原因；模型目录刷新优先经出口池客户端。
- **D-18 管理接口**：`/api/egress` 摘要、`/nodes`、`/nodes/{id}`、`/nodes/{id}/enabled`、`/nodes/{id}/probe`、`/subscriptions`、`/subscriptions/{id}/refresh`、`/events`、`/diagnose`；全部沿用 session + CSRF。`/api/config` 增补 `egress` 读写，订阅 URL 以 `scheme://host/***` 脱敏，提交脱敏 URL 或空 URL 时按 ID 复用原值。
- **验证证据（2026-10-20）**：`go build ./...`、`go vet ./...`、`go test -race ./internal/gateway ./internal/egress/... ./internal/admin ./internal/config` 全部通过；关键用例：跨订阅去重→探测→评分、故障出口→直连切换、单一预算上限、429 冷却、上游共识不冷却节点、SQLite 重开恢复。

---

## D-19 发布形态（2026-10-20）

- 版本 `v0.1.0`；单二进制 + 单 Docker 镜像；五平台交叉编译通过。
- Docker 运行阶段原定 distroless，因 gcr.io 在当前网络不可达改为 `alpine:3.21` + 非 root 用户；仍为单进程、单配置、单 WebUI。
- 前端构建产物提交在 `mesh/internal/webui/dist`，保证无 Node.js 环境也能直接 `go build`；发布 Dockerfile 仍会从源码重建前端。
- 许可证：`mesh/LICENSE`（GPL-3.0 全文，与 o2a 一致）+ `mesh/NOTICE`（Resin MIT 完整声明、opencode2api 来源、Go/前端依赖许可）。
- 升级演练：同一配置/状态目录切换 v0.1.0 二进制，节点状态恢复、健康版本正确；回滚依赖上一版二进制与 `config.json.bak`/手动备份，状态库迁移只向前。

---

## D-20 轮转/刷新/探测纳入统一出口（v0.1.1 后）

- 轮转别名请求原先直接使用 Go Key 的旧代理绑定；现改为与普通推理相同的出口选择器，并上报成功/失败用于节点冷却与 `U_i`。
- 轮转请求的总尝试次数纳入 `retry.max_attempts`；同一模型的多次重试共享 exclude 集合；上游共识时停止切换模型。
- 模型目录刷新与免费模型可用性探测优先使用出口池客户端；这类诊断/目录请求不把模型级 4xx 归因到出口，避免误冷却。
- 验证：`TestRotationFailoverUsesEgress`（429 → 切换模型 → 健康出口成功、单节点冷却、上游 2 次调用）；`TestFreeModelAvailabilityProbeUsesEgress`（探测经出口池、诊断不冷却）；`TestEngineApplyReconcilesSubscriptions`（订阅热更新）；全量 `go test -race ./...` 通过。
