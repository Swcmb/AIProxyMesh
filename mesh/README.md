# Mesh

一个单体服务，融合 **opencode2api** 的完整 OpenCode API 网关能力与 **Resin** 的代理节点订阅、健康评估、出口调度与故障恢复能力。

- 对外暴露 Chat Completions / Responses / Anthropic Messages / SystemOne 协议。
- 统一管理 Zen / Go Key 池、匿名通道、模型目录与模型轮转。
- 代理节点来自 URL 订阅与旧 `proxies` / `proxyfile`，跨订阅按身份去重。
- 出口按健康度、延迟、负载与上游成功率评分；故障自动冷却与切换。
- 一个 Go 可执行文件、一个 Docker 镜像、一套 WebUI（13 页）。

## 架构

```
客户端
  │
  ▼
网关层：认证、协议转换、模型路由、Key 池、单一重试预算
  │
  ▼
调度层：资格过滤 → H/L/R/U 评分 → 加权选择 → 失败归因 → 冷却/共识
  │
  ▼
出口层：direct / HTTP / HTTPS / SOCKS5 / SOCKS5H
  │
  ▼
OpenCode 上游（upstream.zen / go / anonymous）
```

配置仍在 `config.json`；节点、订阅引用、健康、冷却、延迟和切换事件保存在 `egress.state_path` 指向的 SQLite 数据库，二者独立更新。

## 快速开始

### 本地构建

需要 Go 1.25.5+ 与 Node.js 24+（仅前端构建需要）。

```bash
cd mesh/webui && npm ci && npm run build
cd .. && go build -trimpath -ldflags "-s -w" -o mesh ./cmd/mesh
./mesh -config config.json
```

`npm run build` 会把静态资源写入 `internal/webui/dist` 并被 Go 嵌入；运行时不依赖 Node.js。

### Docker

```bash
cd mesh
docker build -t mesh:local .
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v "$PWD/data:/data" mesh:local
```

把 `config.example.json` 复制为 `data/config.json` 并至少修改：

- `server_keys`：客户端访问网关使用的本地 Key。
- `zen_keys` / `go_keys` / `anonymous`：上游凭证。
- `webui.password`：管理端密码（首次启动会哈希后写回）。
- `egress.state_path`：建议放在持久卷内（示例为 `/data/egress.db`）。

默认端口：API `8080`，WebUI `8081`。两者可以分别监听不同地址。

## 配置

完整字段说明见 [`docs/configuration.md`](docs/configuration.md)，示例见 `config.example.json`。

最小生产配置的关注点：

| 配置 | 作用 |
| :--- | :--- |
| `upstream.*` | 唯一的 OpenCode 目标地址来源，不另设 target_domain |
| `retry.max_attempts` | 整个请求的唯一尝试预算；匿名阶段与各 tier 共享 |
| `performance.failure_cooldown_seconds` | 通道层（Key）冷却 |
| `egress.cooldown` | 节点层冷却，指数退避 |
| `egress.failover` | 429/401/403/5xx 策略与 `Retry-After` |
| `egress.probe` | 主动探测间隔、超时、并发与目标（留空从 upstream 派生） |
| `egress.subscriptions` | URL 订阅源；留空仍会导入旧 proxies/proxyfile |

## 管理界面

WebUI 使用 Resin 的设计语言，包含：

1. 总览
2. 模型管理
3. 上游管理
4. 模型轮转
5. API Key 管理
6. 额度看板
7. 代理订阅
8. 代理节点池
9. 请求与切换日志
10. Playground
11. 路由诊断
12. 配置管理
13. 实时监控

管理 API 沿用 opencode2api 的 session + CSRF 体系；新增 `/api/egress/*` 接口。完整列表见 [`docs/api.md`](docs/api.md)。

## 升级与回滚

- **配置**：保存采用原子替换，旧文件保留为 `config.json.bak`。升级前备份 `config.json` 与 `egress.state_path`。
- **二进制**：停止服务 → 替换二进制 → 启动。状态库结构由内嵌迁移自动升级。
- **回滚**：恢复上一版二进制与 `config.json.bak`；状态库建议同时恢复备份（迁移只向前，不做降级迁移）。
- 订阅刷新与配置热重载不会中断进行中的请求。

## 测试

```bash
cd mesh
go build ./...
go test ./...
go test -race ./...
go vet ./...
cd webui && npm run build
docker build -t mesh:test .
```

端到端与故障注入用例位于 `internal/gateway/e2e_test.go`，覆盖三协议、SSE、工具/多模态、Key 轮换、匿名通道、429、5xx 上游共识与全节点故障。

## 目录

```
mesh/
├── cmd/mesh/                # 单入口
├── internal/admin/          # 管理 API 与 CSRF 会话
├── internal/gateway/        # 网关、协议路由、Key 池、重试
├── internal/egress/         # 节点/订阅/解析/出站/探测/调度/SQLite
├── internal/models/         # 模型目录与可用性
├── internal/protocol/       # 三协议转换
├── internal/webui/          # 嵌入的构建产物
├── webui/                   # React + TypeScript 前端源码
└── docs/                    # 配置与 API 文档
```

## 许可证

本项目按 **GPL-3.0-or-later** 分发，完整文本见 `LICENSE`。它衍生自 GPL-3.0-or-later 的 opencode2api，并包含来自 MIT 许可的 Resin 的设计与代码片段；Resin 的版权与许可声明见 `NOTICE`。第三方依赖清单同样在 `NOTICE` 中。
