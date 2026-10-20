# 配置参考

配置文件为 JSON（允许 `//` 注释），使用 `DisallowUnknownFields` 校验；未知字段会导致启动/保存失败。保存时先校验再原子替换，旧文件保留为 `config.json.bak`。

## 顶层字段

| 字段 | 类型 | 说明 |
| :--- | :--- | :--- |
| `listen` | string | API 监听地址，默认 `127.0.0.1:8080` |
| `server_keys` | string[] | 客户端访问网关的本地 Key；至少一个启用的 Key |
| `server_key_metadata` | object | 内部维护的名称/启停/创建时间，不建议手改 |
| `zen_keys` / `go_keys` | string[] | 上游 Key 池 |
| `anonymous` | bool | 启用 OpenCode `public` 匿名通道 |
| `proxies` | string[] | 旧代理列表：`direct` 或 `http(s)://` / `socks5(h)://` |
| `proxyfile` | string | 旧代理文件路径，支持注释与逐行 URL |
| `upstream` | object | `zen` / `go` / `anonymous` 三个上游基址；唯一目标来源 |
| `retry` | object | `max_attempts`（唯一总预算，默认 3）、`timeout_seconds` |
| `models` | object | `refresh_seconds`、`protocols`（模型→原生协议覆盖）、`free_only` |
| `performance` | object | 连接池、连接/尝试/首个 SSE 事件超时、通道冷却 |
| `logging` | object | `level`、`ring_size`、`dump_request_bodies` |
| `prefer` | string | `zen` 或 `go` |
| `reasoning` | object | `effort` / `effort_by_model` 强制推理强度 |
| `rotation` | object | 两个模型别名的名称与首个输出超时；顺序由排名生成 |
| `ranking` | object | 模型综合排名的成本口径、延迟阈值与基准分 |
| `webui` | object | 管理端启用、监听、用户名、密码、会话时长 |
| `egress` | object | 统一出口调度；缺省表示不启用 |

## `egress`

```jsonc
{
  "egress": {
    "enabled": true,
    "subscriptions": [
      { "id": "primary", "name": "primary", "url": "https://example.com/sub", "enabled": true, "refresh_seconds": 3600 }
    ],
    "import_legacy_proxies": true,
    "schedule": {
      "health_weight": 0.30,
      "latency_weight": 0.15,
      "routing_weight": 0.15,
      "upstream_success_weight": 0.40
    },
    "protocols": { "http": true, "https": true, "socks5": true, "socks5h": true },
    "cooldown": { "base_seconds": 30, "max_seconds": 900, "recovery_probe": true },
    "failover": {
      "status_policy": { "429": "switch-or-wait", "401": "key-rotation", "403": "key-rotation", "5xx": "retry-or-switch" },
      "request_method_policy": { "default": "retry-allowed" },
      "honor_retry_after": true
    },
    "probe": { "targets": [], "interval_seconds": 60, "timeout_seconds": 10, "concurrency": 8 },
    "state_path": "/data/egress.db",
    "max_latency_entries": 128
  }
}
```

| 字段 | 说明 |
| :--- | :--- |
| `enabled` | 是否启用统一出口池 |
| `subscriptions[].id` | 唯一 ID；跨订阅同节点按配置哈希去重 |
| `subscriptions[].url` | 订阅 URL，仅支持 http/https；支持纯 URI、Base64、Clash 最小子集、sing-box/Clash JSON |
| `subscriptions[].refresh_seconds` | 0 表示仅手动刷新；>0 时最小 30 秒 |
| `import_legacy_proxies` | 默认 true；把 `proxies` / `proxyfile` 作为 `legacy` 源导入 |
| `schedule.*` | H/L/R/U 权重，自动归一化；全零使用默认值 |
| `protocols.*` | 关闭的协议保留节点可见但不参与调度 |
| `cooldown.base_seconds` / `max_seconds` | 节点层指数退避与上限；`Retry-After` 可超出该上限（硬上限 24h） |
| `cooldown.recovery_probe` | 保留字段；当前主动探测始终运行 |
| `failover.status_policy` | 策略映射；429/401/403/5xx 的默认语义见上 |
| `failover.request_method_policy` | 方法级重试策略，默认允许 |
| `failover.honor_retry_after` | 是否解析并遵循 `Retry-After` |
| `probe.targets` | 留空时从 `upstream.zen/go/anonymous` 派生；探测只到 origin |
| `state_path` | SQLite 路径；默认 `config.json.egress.db` |
| `max_latency_entries` | 每节点延迟表上限（1–4096） |

## `rotation` 与 `ranking`

```jsonc
{
  "rotation": {
    "sota": { "alias": "sota" },
    "sweet": { "alias": "sweet" },
    "first_output_seconds": 30
  },
  "ranking": {
    "sota_task": "agent-coding",
    "sweet_task": "general-text",
    "min_context_tokens": 0,
    "latency_good_ms": 2000,
    "latency_bad_ms": 15000,
    "standard_input_tokens": 60000,
    "standard_output_tokens": 8000,
    "standard_cached_tokens": 20000,
    "go_monthly_cost_usd": 10,
    "go_monthly_tasks": 1000,
    "quota_headroom": { "go": 100, "zen": 100 },
    "capabilities": {
      "glm-5.3": { "l": 88, "b": 82, "i": 79 },
      "go:kimi-k3": { "l": 91 }
    }
  }
}
```

| 字段 | 说明 |
| :--- | :--- |
| `rotation.*.alias` | 对外别名；`first_output_seconds` 为单次尝试等待首个有效输出的上限（1–300 秒） |
| 模型顺序 | 无需配置，由排名引擎按 `ranking.*_task` 对应的任务预设自动生成 |
| `sota_task` / `sweet_task` | `agent-coding`（默认）、`general-text`、`vision` |
| `min_context_tokens` | 大于 0 时覆盖任务预设的上下文硬门槛 |
| `latency_good_ms` / `latency_bad_ms` | P95 延迟线性打分区间，需满足 `0 < good < bad` |
| `standard_*_tokens` | 标准任务的输入 / 输出 / 缓存读取 Token 数，用于把公开价格换算成单任务成本 |
| `go_monthly_cost_usd` / `go_monthly_tasks` | Go 订阅费按月分摊；为 0 时改用发布价格估算 |
| `quota_headroom` | 各提供商 0–100 的额度余量，缺省 100 |
| `capabilities` | 模型基准分 `l`（排行榜）、`b`（专项基准）、`i`（内部实测），0–100；`provider:model` 键优先于裸模型 ID |

综合得分 `S = G × (wQ·Q + wE·E + wA·A + wF·F)`；三套预设权重与准入条件见 `project-context/specs/model-ranking.md`。缺失的基准分按剩余权重归一化，未测量维度按中性 50 分计入并降低置信度，样本量不足不会冲到榜首。

## 兼容与迁移

- 旧 `proxies` / `proxyfile` 不需要改写：启用 egress 且 `import_legacy_proxies` 为 true 时自动进入节点池。
- `rotation.sota.order` / `rotation.sweet.order` 已移除：手写顺序由排名引擎取代，旧配置文件里保留该字段会启动失败，请删除。
- 不新增目标域名配置；调度与探测均使用 `upstream.*`。
- 订阅 URL 在管理 API 中只暴露 `scheme://host/***`；提交空值或该脱敏值时按订阅 ID 复用原 URL。
- 配置与节点状态分离：订阅刷新、健康变化不会重写 `config.json`。
- 删除订阅后，其独占节点会在启动与配置热更新时从状态库清除（`egress_orphan_nodes_removed` 日志）；被其他订阅共享的出口保留。状态库只缓存健康数据，订阅清单始终以 `config.json` 为准。

## 状态库

SQLite（modernc，纯 Go）通过内嵌迁移自动建表：

- 节点身份/原始配置/手动启停/连续失败/冷却
- 订阅引用
- 每节点每目标延迟（TD-EWMA）
- 每节点每目标上游成功/失败计数
- 出口切换事件（上限 5000 条，周期裁剪）

升级前请备份该文件。迁移只向前，不支持降级迁移。
