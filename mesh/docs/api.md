# API 参考

## 1. 网关 API

认证：`Authorization: Bearer <server_key>` 或 `x-api-key: <server_key>`。
`GET /healthz` 不需要认证。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/v1/models` | 对外暴露的模型列表 |
| POST | `/v1/chat/completions` | OpenAI Chat Completions（JSON / SSE） |
| POST | `/v1/responses` | OpenAI Responses（JSON / SSE） |
| POST | `/v1/messages` | Anthropic Messages（JSON / SSE） |
| POST | `/v1/systemone` | SystemOne 结构化决策端点，原样透传 |
| GET | `/healthz` | 网关健康与就绪状态 |

协议转换始终把请求编码为所选 tier 的原生协议，再把响应转换回调用方协议。流式响应一旦向客户端发出有效输出，不会重放或拼接第二份回答。

## 2. 管理 API

WebUI 监听器同源提供。认证使用 HttpOnly Session Cookie `mesh_session`；除登录外均需会话。所有写操作还需请求头 `X-CSRF-Token`，并校验 `Origin`（若提供）。

错误响应：

```json
{ "error": { "code": "invalid_request", "message": "..." } }
```

### 认证

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| POST | `/api/auth/login` | `{username,password}` → `{username,csrf_token,expires_at}`，设置 Cookie |
| GET | `/api/auth/session` | 当前会话与 CSRF |
| POST | `/api/auth/logout` | 注销（需 CSRF） |

### 配置与账号

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/config` | 脱敏配置（密钥仅显示尾号，订阅 URL 仅显示 origin） |
| PUT | `/api/config` | 完整配置替换；密钥用 `{id}` 保留或 `{value}` 新增；`egress` 可读写 |
| POST | `/api/config/reload` | 从磁盘重新加载并热切换 |
| POST | `/api/config/reveal` | `{password}` 验证后返回完整密钥 |
| PUT | `/api/account` | 修改 WebUI 用户名/密码（需当前密码） |

### 监控与诊断

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/monitor` | 运行指标、Token 用量、上游尝试、资源快照 |
| GET | `/api/debug/models` | 模型路由诊断、Key 状态、目录/元数据 |
| POST | `/api/debug/inference` | Playground；诊断请求不改变 Key/节点冷却 |
| GET | `/api/models/availability` | 免费模型可用性 |
| POST | `/api/models/restore` | `{model}` 恢复自动探测 |
| POST | `/api/models/manual` | `{model,manual}` 切换手动可用性 |
| GET | `/api/model-quotas` | Go 共享套餐额度（只读百分比） |
| GET | `/api/logs` | 近期结构化日志（`after`/`limit`） |
| GET | `/api/logs/stream` | 管理面 SSE 日志流 |

### 模型轮转

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/rotation` | 分组、顺序、当前模型、暂停/可用、切换记录 |
| PUT | `/api/rotation` | `{sota:{alias,order},sweet:{alias,order},first_output_seconds}` |
| POST | `/api/rotation/current` | `{group,model}` 手动切换 |

### API Key

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/api-keys` | 列表（含累计/近 1 小时用量） |
| POST | `/api/api-keys` | `{name,value?}`；value 为空自动生成 |
| PATCH | `/api/api-keys/{id}` | `{name?,enabled?}` |
| DELETE | `/api/api-keys/{id}` | 删除；至少保留一个启用的 Key |

### 出口调度 `/api/egress/*`

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/egress` | 快照：计数、节点、订阅状态；未启用时 `{enabled:false}` |
| GET | `/api/egress/nodes` | 节点池分页查询；参数 `keyword`、`status`、`subscription`、`protocol`、`region`、`sort_by`、`sort_order`、`offset`、`limit`（默认 50，上限 5000）。响应含 `nodes`、`total`（筛选后条数）、`pool`（池内总数）、`counts`、`facets`、`subscription_names` |
| GET | `/api/egress/nodes/{id}` | 单节点详情，含 `status`、`score`（H/L/R/U）与 `eligible` |
| POST | `/api/egress/nodes/{id}/enabled` | `{enabled:bool}` 手动启停 |
| POST | `/api/egress/nodes/{id}/probe` | 立即探测并返回刷新后的节点 |
| GET | `/api/egress/subscriptions` | 订阅状态（节点数、格式、错误、下次刷新）；URL 仅显示 origin |
| POST | `/api/egress/subscriptions` | 新增订阅 `{id,name,url|content,enabled,refresh_seconds}` |
| GET | `/api/egress/subscriptions/{id}` | 单订阅原文（未脱敏 URL 与粘贴内容），供运维核对自己登记的地址 |
| PATCH | `/api/egress/subscriptions/{id}` | 局部更新；URL 传掩码值即沿用已存地址，传 content 会切为粘贴源 |
| DELETE | `/api/egress/subscriptions/{id}` | 删除订阅；其独占节点在下一次刷新时移出池 |
| POST | `/api/egress/subscriptions/{id}/refresh` | 立即刷新订阅 |
| POST | `/api/egress/subscriptions/{id}/cleanup` | 清理该订阅处于冷却中的出口并立即刷新，返回 `{removed_nodes:n}` |
| GET | `/api/egress/events` | 出口切换事件（`limit`，默认 200） |
| GET | `/api/egress/diagnose` | `target=` 指定上游 URL；返回每个节点的资格、原因与 H/L/R/U 分数 |

`status` 取值：`healthy`、`cooling`、`error`、`unprobed`、`disabled`、`unsupported`；`sort_by` 取值：`score`（默认）、`name`、`latency`、`health`、`failures`、`last_probe_at`、`created_at`。

### 地区解析 `/api/egress/geoip*`

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/egress/geoip` | 国家库状态；未启用时 `{enabled:false}` |
| POST | `/api/egress/geoip/update` | 立即下载并校验数据库，成功后热替换 |
| GET | `/api/egress/geoip/lookup?ip=` | 查询单个 IP 的国家归属 |

节点视图在有库时附带 `region`（ISO 3166-1 国家码）与 `region_name`（中文名），`facets.regions` 列出当前筛选可选的国家，`facets.geoip_enabled` 表示是否启用。

节点 ID 为 32 位十六进制配置哈希；同一出口出现在多个订阅时共享同一 ID 与健康状态，并在 `aliases` 中列出每个订阅给它的名字。

## 3. 排名 API

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/api/rankings` | 模型×接入路线综合排名；`task=agent-coding\|general-text\|vision`，缺省 `agent-coding` |
| GET | `/api/rankings/tasks` | 可用任务预设、权重与准入条件 |

排名对象是「提供商 + 模型 + 任务」组合。响应包含 `gate`、`Q/E/A/F`、`final_score`、`sample_count`、`confidence` 与明细分量；`gate=false` 的路线保留展示但不参与排序。

## 4. 兼容性说明

- 管理路由统一使用 `mesh_session` + CSRF，无第二套认证体系。
- 已删除的多接入点、请求头规则、粘性租约、反向代理等模块不提供接口。
