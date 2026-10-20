# 阶段 6 故障注入报告

| 项 | 值 |
| :--- | :--- |
| 日期 | 2026-10-20 |
| 范围 | 出口选择、失败归因、两层冷却、单一尝试预算、上游共识、SSE/客户端取消 |
| 执行 | `cd mesh && go test ./internal/gateway ./internal/egress/... -count=1 -v`；`go test -race ./...` |
| 测试文件 | `mesh/internal/gateway/e2e_test.go`、`mesh/internal/gateway/egress_test.go`、`mesh/internal/egress/routing/router_test.go` |

## 1. 故障场景与结果

| # | 注入 | 期望 | 实测用例 | 结果 |
| :--- | :--- | :--- | :--- | :--- |
| 1 | 代理端口拒绝连接 | 节点层冷却、切换其他出口；不冷却 Key | `TestEgressFailoverToHealthyExit` | 2 次尝试，第 2 次经 direct 成功；故障节点冷却；健康节点 0 失败 |
| 2 | 全部出口连接失败 | 尝试次数 ≤ `retry.max_attempts`，全部节点冷却 | `TestEgressAllNodesFailWithinBudget` | used=2 ≤ 3；上游 0 命中；两个节点冷却 |
| 3 | 出口返回 429 + `Retry-After: 2` | 该出口冷却、切换健康出口；遵循等待语义 | `TestEgress429SwitchesExit` | 2 次尝试；上游仅 1 次；429 节点冷却；`Retry-After` 生效 |
| 4 | `Retry-After` 大于冷却上限 | 不被 `cooldown.max_seconds` 截断 | `TestRetryAfterBeyondCooldownCap` | 1 小时 Retry-After → 冷却 1 小时 |
| 5 | 两个不同出口对同一上游均 5xx | 上游共识、停止遍历、节点不冷却；若先前已按出口冷却则撤销 | `TestEgressUpstreamWideStopsWithoutCoolingNodes`、`TestExitFailureConsensusBecomesUpstreamWide` | used=2；返回 503；共识后两个节点均未冷却（先前 429 冷却被清除） |
| 6 | 单出口 5xx 且无其他出口成功 | 不归因于出口（继续重试，不冷却节点） | `TestFiveHundredAttribution` | 无共识时无节点冷却 |
| 7 | 401/403 | 只走 Key 通道冷却，不惩罚代理 | `TestKeyFailureDoesNotPenalizeExit`、`TestGatewayKeyRotation` | 401 后切换 Key；节点不冷却；第 2 个 Key 成功 |
| 8 | 不可重试 400 | 立即返回，不轮换 Key/节点 | `TestNonRetryableClientResponseStopsImmediately` | used=1；上游 1 次 |
| 9 | 客户端取消 | 不冷却、不计数重试 | `TestClientCancelDoesNotPenalize` | 状态不变 |
| 10 | 同轮重复节点 | 不得重复尝试同一节点 | `TestSelectRequiresEligibleNode`（exclude） | 排除后返回 `ErrNoEligible` |
| 11 | 冷却到期恢复 | 到期后重新参加调度 | `TestCooldownBackoffAndRetryAfter`、`TestEnginePersistsNodeState` | 到期后可选中；重启后冷却仍在 |
| 12 | 轮转别名 429 | 模型切换；故障出口冷却，下一模型使用健康出口；总尝试不超预算 | `TestRotationFailoverUsesEgress` | 200；`X-Resolved-Model` 切换；1 个出口冷却；上游 2 次调用 |
| 13 | 并发请求 | 无数据竞争、预算独立 | `go test -race ./...` | 通过 |

## 2. 预算与分层断言

- **单一预算池**：匿名阶段 + 各 tier 共享 `retry.max_attempts`。`TestKeyUpstreamSingleBudgetLegacy` 断言 used 与上游命中都恰好等于 3；`TestEgressAllNodesFailWithinBudget` 断言 used ≤ 3。
- **换出口消耗一次尝试**：每次 `doKeyUpstream` 循环仅在真正发起请求时 `attempts++`；选择失败/等待不计数。
- **同轮去重**：`exclude` 集合以节点 ID 记录本轮已尝试出口。
- **冷却分层**：节点层冷却仅由 `ProxyFailure` / 单出口 `ExitFailure` 触发；`KeyFailure` 与 `UpstreamWide` 仅触发 Key 冷却；`ClientCancel` 与 `Neutral` 不触发任何冷却。
- **direct 语义**：无代理跳的直连传输失败归为 `UpstreamFailure`（上游不可达），不冷却 direct 节点。
- **重试边界**：SSE 首输出前才允许重试；客户端取消/请求上下文过期时释放出口槽位但不记结果。
- **轮转路径**：sota/sweet 别名请求经同一出口选择器；同一模型的多次重试共享 exclude 集合，总尝试次数受 `retry.max_attempts` 约束；上游整体故障时停止切换模型。

## 3. 运行命令

```bash
cd mesh
go test ./internal/gateway -run 'TestEgress|TestGatewayKeyRotation|TestAnonymousLane|TestSystemOne|TestNonRetryable' -count=1 -v
go test ./internal/egress/... -count=1
go test -race ./... -count=1
```

## 4. 结论

故障注入全部通过：出口故障会被冷却并切换，健康出口不被误伤；429 遵循 `Retry-After`；上游整体故障在双出口共识后停止且不冷却节点；尝试次数受单一预算约束；并发 race 检查通过。
