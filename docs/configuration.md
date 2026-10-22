# 配置参考

配置文件为 JSON（允许 `//` 注释），使用 `DisallowUnknownFields` 校验；未知字段会导致启动/保存失败。保存时先校验再原子替换，旧文件保留为 `config.json.bak`。

## 顶层字段

| 字段 | 类型 | 说明 |
| :--- | :--- | :--- |
| `listen` | string | API 监听地址，默认 `127.0.0.1:8080` |
| `server_keys` | string[] | 客户端访问网关的本地 Key；至少一个启用的 Key |
| `server_key_metadata` | object | 内部维护的名称/启停/创建时间，不建议手改 |
| `zen_keys` / `go_keys` | string[] | 上游 Key 池 |
| `anonymous` | bool | 启用 OpenCode `public` 匿名通道；缺省为开，显式 `false` 关闭 |
| `proxies` | string[] | 旧代理列表：`direct` 或 `http(s)://` / `socks5(h)://` |
| `proxyfile` | string | 旧代理文件路径，支持注释与逐行 URL |
| `upstream` | object | `zen` / `go` / `anonymous` 三个上游基址；唯一目标来源 |
| `retry` | object | `max_attempts`（唯一总预算，默认 3）、`timeout_seconds` |
| `models` | object | `refresh_seconds`、`protocols`（模型→原生协议覆盖）、`free_only`、`anonymous_backoff`（匿名通道熔断） |
| `performance` | object | 连接池、连接/尝试/首个 SSE 事件超时、通道冷却 |
| `logging` | object | `level`、`ring_size`、`dump_request_bodies`、`requests`（持久请求日志） |
| `prefer` | string | `zen` 或 `go` |
| `reasoning` | object | `effort` / `effort_by_model` 强制推理强度 |
| `rotation` | object | 两个模型别名的名称与首个输出超时；顺序由排名生成 |
| `ranking` | object | 模型综合排名的成本口径、延迟阈值与基准分 |
| `webui` | object | 管理端启用、监听、用户名、密码、会话时长 |
| `egress` | object | 统一出口调度；缺省表示不启用 |

## 匿名通道与请求级拒绝

- **匿名通道熔断**：配置 `models.anonymous_backoff` 后，匿名通道连续失败 `threshold` 次（默认 6）即暂停整个通道；暂停从 `base_hours`（默认 1）起步，连续触发翻倍，上限 `max_hours`（默认 24）。暂停期间不抽出口、不发上游请求，直接进入 key tier；任一次成功会关闭熔断并清零计数。缺省不启用。

  ```jsonc
  "models": {
    "anonymous_backoff": { "threshold": 6, "base_hours": 1, "max_hours": 24 }
  }
  ```

- **请求级拒绝**：上游返回 400/422 且正文包含 `invalid_request_error`、`context_length_exceeded`、`maximum context length`、`string_above_max_length` 或 `prompt is too long` 时，说明请求本身无法被任何出口或 Key 接受，网关立即结束并原样返回该响应，不再重试。401/403/429 仍按凭证或出口问题处理。

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
| `subscriptions[].url` | 订阅 URL，仅支持 http/https；支持纯 URI、Base64、hproxy CSV、Clash 最小子集、sing-box/Clash JSON |
| `subscriptions[].content` | 粘贴的节点清单，与 `url` 互斥；粘贴源不定时刷新，保存时解析一次 |
| `subscriptions[].refresh_seconds` | 0 表示仅手动刷新；>0 时最小 30 秒；粘贴源必须为 0 |
| `subscriptions[].protocols` | 可选白名单（`http`/`https`/`socks5`/`socks5h`）；配置后该订阅只导入列出的协议，留空沿用全局 `egress.protocols` |
| `subscriptions[].ephemeral` | 临时源标记；开启后连续熔断超过 `evict_delay_seconds` 的节点会从该订阅驱逐并减引用（默认 600 秒，范围 60–86400） |
| `subscriptions[].evict_delay_seconds` | 临时源的驱逐延迟；仅在 `ephemeral: true` 时生效 |
| `import_legacy_proxies` | 默认 true；把 `proxies` / `proxyfile` 作为 `legacy` 源导入 |
| `schedule.*` | H/L/R/U 权重，自动归一化；全零使用默认值 |
| `protocols.*` | 关闭的协议保留节点可见但不参与调度 |
| `cooldown.base_seconds` / `max_seconds` | 节点层指数退避与上限；`Retry-After` 可超出该上限（硬上限 24h） |
| `cooldown.recovery_probe` | 保留字段；当前主动探测始终运行 |
| `failover.status_policy` | 策略映射；429/401/403/5xx 的默认语义见上 |
| `failover.request_method_policy` | 方法级重试策略，默认允许 |
| `failover.honor_retry_after` | 是否解析并遵循 `Retry-After` |
| `probe.targets` | 留空时从 `upstream.zen/go/anonymous` 派生；必须保留 base path，`/zen` 与 `/zen/go` 是不同健康键 |
| `probe.egress_ip_enabled` | 是否启用出口 IP 探测（默认 true）；结果写入节点 `egress_ip`，地区优先按真实出口解析 |
| `probe.egress_ip_interval_seconds` | 出口 IP 探测周期（60–604800，默认 21600）；每 13–17 秒抖动扫描，每轮最多入队 250 个 |
| `probe.egress_ip_target` | 出口 IP 探测地址；默认 `https://cloudflare.com/cdn-cgi/trace`，解析响应中的 `ip=` 行 |
| `probe.canary_enabled` | 是否启用推理金丝雀（默认 true）；关闭后只依赖真实流量积累证据 |
| `probe.canary_model` | 新可达节点没有健康键时用于生成候选键的模型；留空则只靠真实流量建立键 |
| `probe.canary_default_mode` | 金丝雀请求模式（`stream`/`json`，默认 stream） |
| `probe.budget_per_second` / `budget_burst` | 金丝雀全局令牌桶（默认 2/s，突发 4） |
| `probe.canary_daily_budget` | 金丝雀每日上限（默认 20000），按 UTC 零点重置 |
| `probe.canary_max_inflight` | 全局在途金丝雀上限（默认 8）；同一键另有单飞约束 |
| `probe.active_recheck_seconds` / `verifying_recheck_seconds` | 稳定键与验证键的主动复验下限（预算允许时才执行） |
| `bootstrap.mode` | `direct`（默认）或 `http_connect`；Mesh 到节点端点始终显式拨号，不读系统代理 |
| `bootstrap.url` | `http_connect` 模式的 HTTP CONNECT 代理；必填 |
| `bootstrap.probe_url` | 经 bootstrap 路径探测的地址；其延迟不进入节点 TTFB |
| `health.mode` | `shadow`（默认，只记录证据）、`prefer`（稳定池优先 + 探索）、`strict`（只走 ACTIVE） |
| `health.min_samples` / `valid_min_samples` | 准入最小样本；默认 96 / 46，并受 alpha-spending 首检约束 |
| `health.exploration_ratio` / `candidate_exploration_ratio` | `prefer` 模式的探索配额（默认 10%，其中 2% 给候选） |
| `health.max_keys_per_node` | 每节点健康键上限（1–32，默认 4）；稳定态不参与淘汰 |
| `state_path` | SQLite 路径；默认 `config.json.egress.db` |
| `max_latency_entries` | 每节点延迟表上限（1–4096） |
| `geoip` | 国家地区库配置；该段存在即启用地区解析，`{"enabled": false}` 关闭 |
| `geoip.url` | 数据库下载地址，仅 http/https；默认 MetaCubeX meta-rules-dat 的 `country.mmdb` 镜像 |
| `geoip.sha256` | 期望摘要（64 位十六进制）；留空则只校验格式与大小 |
| `geoip.path` | 本地库路径；默认 `<state_path>.geoip.mmdb` |
| `geoip.refresh_days` | 自动更新周期（0–365 天），默认 7 天 |

### `egress.bootstrap`

Mesh 到节点端点与节点到上游是两条独立链路。`direct` 直接拨号并忽略 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY`；`http_connect` 先与 `bootstrap.url` 建立 CONNECT 隧道，再在隧道内连接节点。v1 的 `http_connect` 只支持 HTTP/HTTPS 节点，SOCKS5 节点会跳过并记录 `unsupported_bootstrap_protocol`。

bootstrap 失败或本地网络异常归入环境状态，不冷却节点：连续 `probe_failure_threshold` 次失败或本地失败率超过 `local_failure_threshold` 且持续 `failure_grace_seconds` 后冻结健康更新；恢复时递增 `environment_epoch`，ACTIVE 键保留路由资格但标记待复验。

### `egress.health`

健康键是「上游 × 模型 × 协议 × 请求模式」。每个键独立维护 HTTP 200、有效推理、主指标、延迟和七态状态机：

| 状态 | 路由 | 说明 |
| :--- | :--- | :--- |
| `candidate` | 仅冷启动回退 | 尚无 HTTP 200 证据 |
| `verifying` | 受限探索 | 已收到 HTTP 200，样本未达准入 |
| `active` | 是 | Wilson 下界、延迟与有效推理均达标 |
| `suspect` | 是，权重 ×0.3 | 近期分位、连续失败、流失败或延迟恶化 |
| `open` | 否 | 键级熔断，到期进入半开 |
| `half_open` | 单在途探测 | 连续成功恢复到之前状态 |
| `dead` | 否 | 同键重复 404 且其他出口可用 |

有效推理：Chat/Responses/Anthropic 首批输出事件必须可解析且不含错误；`systemone` 只校验 HTTP 200。201/204 不计入 HTTP 200 成功率。地区 403 是硬性封锁，过滤后为空时返回明确错误，不回退到被封锁地区。

### `egress.geoip`

地区解析用 MaxMind 兼容的 `country.mmdb`：

- 启动时若本地已有可用库则直接加载；缺失或超过 `refresh_days` 时后台下载。
- 下载先写临时文件，校验摘要、大小（≥1 MiB）并能以 MaxMind 格式打开后才原子替换，坏下载不会让已生效的库下线。
- 解析结果用于节点池的 `region` / `region_name` 字段与地区筛选；数据库缺失时该列为空，节点仍全部照常展示。
- 地址与路径只来自本配置，不接受 API 传入，因此该下载链路不会被请求方指向任意主机。
- 管理端可在节点池页查看库状态、手动触发更新与查询单个 IP 的归属。

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

## 持久请求日志

`logging.requests` 用独立的 SQLite 库（默认 `<config>.requests.db`）保存每次上游请求与每次上游尝试，重启后历史仍在：

| 字段 | 说明 |
| --- | --- |
| `enabled` | 缺省开启；显式 `false` 可关闭，关闭时只保留内存环形缓冲 |
| `state_path` | 请求库路径；默认 `<config>.requests.db` |
| `retention_days` | 按时间裁剪（1–3650），默认 7 天 |
| `max_rows` | 按条数裁剪，保留最新的 N 条请求，默认 200000 |
| `buffer_size` | 写入缓冲条数；0 用默认 8192 |

日志库初始为空，只记录真实请求，不预置任何示例或默认记录。

写入路径不阻塞请求：记录先进入内存缓冲，后台按 100 条或 500ms 批量落盘。缓冲满时丢弃新记录并在 `dropped` 计数中体现，宁可丢历史也不拖慢推理。请求与尝试分表存储，尝试按 `request_id` 关联，详情页展示完整尝试链。

`enabled` 与 `state_path` 改变会重开数据库；`retention_days` 与 `max_rows` 热更新即时生效。
