# Mesh

一个单体 OpenCode API 网关，内置统一代理出口池、健康评估、智能调度与模型排名。

- 对外提供 Chat Completions、Responses、Anthropic Messages 与 SystemOne。
- 管理 Zen / Go Key 池、匿名通道、模型目录、模型排名与轮转。
- 代理节点来自 URL 订阅与旧 `proxies` / `proxyfile`，跨订阅按身份去重。
- 出口按健康度、延迟、负载与上游成功率评分；故障自动冷却与切换。
- 模型按 `G × (Q/E/A/F)` 排名：能力、成本、可用性、任务适配度与硬性准入。
- 一个 Go 可执行文件、一个 Docker 镜像、一套 WebUI。

## 架构

```
客户端
  │
  ▼
网关层：认证、协议转换、模型路由、Key 池、单一重试预算
  │
  ▼
调度层：资格过滤 → 出口评分 → 加权选择 → 失败归因 → 冷却/共识
  │
  ▼
出口层：direct / HTTP / HTTPS / SOCKS5 / SOCKS5H
  │
  ▼
上游服务（upstream.zen / go / anonymous）
```

模型排名引擎独立于出口调度：它按「模型 + 任务类型」计算综合分，模型管理页的列表顺序、路由诊断与轮转顺序都由它提供依据。

配置在 `config.json`；节点、订阅、健康、冷却、延迟与切换事件保存在 `egress.state_path` 指向的 SQLite 数据库。

## 快速开始

需要 Go 1.25.5+ 与 Node.js 24+（仅前端构建需要）。

```bash
cd mesh/webui && npm ci && npm run build
cd .. && go build -trimpath -ldflags "-s -w" -o mesh ./cmd/mesh
./mesh -config config.json
```

`npm run build` 把静态资源写入 `internal/webui/dist` 并嵌入 Go；运行时不依赖 Node.js。

### Docker

```bash
cd mesh
docker build -t mesh:local .
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v "$PWD/data:/data" mesh:local
```

把 `config.example.json` 复制为 `data/config.json`，至少修改 `server_keys`、上游 Key、`webui.password` 与 `egress.state_path`。

## 模型排名

排名对象是「模型 + 任务类型」。Zen 与 Go 是同一模型的两条接入路线，排名阶段合并：费用取最便宜路线，可用性与能力取最好值，成功率与延迟按渠道汇总。

模型管理页的模型列表按所选任务预设的排名顺序排列，并在首列显示名次与综合分；未通过硬性准入的模型排在最后并显示原因。

```
S = G × (0.40Q + 0.20E + 0.25A + 0.15F)
Q = 0.50L + 0.30B + 0.20I          # 排行榜 / 专项基准 / 内部实测
E = 100 / (1 + C / C_ref)          # 单任务有效成本
A = 0.60U + 0.25La + 0.15H         # 成功率 / P95 延迟 / 额度余量
F = 0.50M + 0.25Cx + 0.25To        # 模态 / 上下文 / Agent 能力
G ∈ {0,1}                          # 硬性准入
```

默认任务预设：`agent-coding`（40/20/25/15）、`general-text`（35/25/25/15）、`vision`（35/15/25/25）。缺失的 Q 指标会按其余可用指标重新归一化；实测样本不足时降低置信度。完整定义见 `project-context/specs/model-ranking.md`。

## 管理界面

1. 总览
2. 模型管理（按综合排名排序）
3. 上游管理
4. 模型轮转
5. API Key
6. 额度看板
8. 代理订阅
9. 代理节点池
10. 请求与切换日志
11. Playground
12. 路由诊断
13. 配置管理
14. 实时监控

管理 API 使用 session + CSRF；`/api/egress/*` 与 `/api/rankings` 沿用同一认证。完整列表见 [`docs/api.md`](docs/api.md)。

## 配置

完整字段见 [`docs/configuration.md`](docs/configuration.md)，示例见 `config.example.json`。

| 配置 | 作用 |
| :--- | :--- |
| `upstream.*` | 上游地址唯一来源 |
| `retry.max_attempts` | 整个请求的唯一尝试预算 |
| `performance.failure_cooldown_seconds` | 通道层（Key）冷却 |
| `egress.cooldown` | 节点层冷却，指数退避 |
| `egress.failover` | 429/401/403/5xx 策略与 `Retry-After` |
| `egress.probe` | 主动探测间隔、超时、并发与目标 |
| `egress.subscriptions` | URL 订阅源；留空仍会导入旧 proxies/proxyfile |
| `ranking.*` | 任务权重、能力分输入、标准任务 Token、延迟阈值与额度余量 |

## CI 与自动更新

- `.github/workflows/checks.yml`：Ubuntu/Windows × Go 1.25.x/stable 的 vet/build 矩阵、gofmt、WebUI 构建、test/race。
- `.github/workflows/release.yml`：`v*` 标签构建六平台产物、GHCR 多架构镜像与 GitHub Release。
- `.github/workflows/update-dependencies.yml`：每天更新 Go 补丁依赖与前端依赖，验证后推送 `main` 并显式触发 checks。
- 失败邮件：仓库 Secrets 配置 `SMTP_SERVER`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`NOTIFY_EMAIL`；可选 `SMTP_PORT`、`SMTP_SECURE`、`SMTP_FROM`。未配置时跳过邮件，可在 GitHub 通知设置中开启 Actions 失败邮件。

## 升级与回滚

- 配置保存采用原子替换，旧文件保留为 `config.json.bak`。
- 升级前备份 `config.json` 与 `egress.state_path`；二进制替换后启动即可，状态库自动迁移。
- 回滚：恢复上一版二进制与配置备份；状态库迁移只向前，建议同时恢复备份。
- 订阅刷新与配置热重载不会中断进行中的请求。

## 测试

```bash
cd mesh
go build ./...
go test ./...
go test -race ./...
go vet ./...
cd webui && npm run build
```

端到端与故障注入用例位于 `internal/gateway/e2e_test.go`。

## 许可证

本项目按 **GPL-3.0-or-later** 分发，完整文本见 `LICENSE`。第三方组件的版权与许可证声明见 `NOTICE`。
