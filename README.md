# Mesh

单体 OpenCode API 网关：统一代理出口池、健康评估、智能调度与模型排名，内置 Web 管理界面。

- 对外提供 Chat Completions、Responses、Anthropic Messages 与 SystemOne 协议。
- 管理 Zen / Go Key 池、匿名通道、模型目录、模型排名与轮转顺序。
- 代理节点来自 URL 订阅与旧 `proxies` / `proxyfile`，跨订阅按配置哈希去重，健康状态共享。
- 出口按健康度、延迟、负载与上游成功率评分；故障自动冷却与切换，新节点首次连通前不参与调度。
- 模型按 `G × (Q/E/A/F)` 排名：能力、成本、可用性、任务适配度与硬性准入先行。
- 一个 Go 可执行文件、一个 Docker 镜像、一套 WebUI；配置与状态分离，状态库自动迁移。

## 安装

### npm

```bash
npm install -g @ianwusb/mesh
mesh -config config.json
```

安装时按平台自动获取预编译二进制（linux/darwin/win32 × x64/arm64），无需 Go 环境。

### GitHub Release

从 [Releases](https://github.com/Swcmb/AIProxyMesh/releases) 下载对应平台压缩包（附 sha256），解压后直接运行 `mesh`。

### Docker

```bash
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v "$PWD/data:/data" ghcr.io/swcmb/aiproxymesh:latest
```

镜像为多架构（linux/amd64、linux/arm64），标签含版本号、`major.minor` 与 `latest`。

### Go install

```bash
go install github.com/Swcmb/AIProxyMesh/mesh/cmd/mesh@latest
```

### 源码构建

需要 Go 1.26.10+；修改前端时需要 Node.js 24+。

```bash
cd webui && npm ci && npm run build   # 产物写入 internal/webui/dist 并嵌入 Go
cd .. && go build -trimpath -ldflags "-s -w" -o mesh ./cmd/mesh
./mesh -config config.json
```

运行时不依赖 Node.js。

## 快速开始

1. 复制 `config.example.json` 为 `config.json`。
2. 至少修改：`server_keys`（对外接入 Key）、`zen_keys` / `go_keys`（上游 Key）、`webui.password`（管理密码）、`egress.state_path`（状态库路径）。
3. 启动后访问 `http://127.0.0.1:8081` 进入管理界面；API 监听 `8080`。
4. 在「代理订阅」页添加订阅或启用旧 `proxies` 导入，节点池即可参与调度。

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

- **认证**：管理端统一 session + CSRF；`/api/egress/*` 与 `/api/rankings` 沿用同一认证。
- **冷却分层**：节点层 `egress.cooldown`（指数退避，`Retry-After` 可延长）与通道层 Key 冷却独立，同一失败只触一层。
- **探测体系**：主动探测采用双优先级固定 worker 池与 13–17 秒抖动扫描；真实流量被动上报连通性与 TLS 握手延迟；出口 IP 低频探测并优先用于地区解析；`ephemeral` 订阅会驱逐长期熔断的节点。网络层 403 地区限制会按出口国家硬性隔离并自动换出口。
- **健康准入**：按「出口 × 上游 × 模型 × 协议 × 模式」统计 HTTP 200 与有效推理，Wilson 下界 + alpha-spending 准入、衰减 Beta 近期健康、七态键级熔断；金丝雀只在证据不足或恢复时按预算补测。默认 `shadow` 只记录证据，`prefer`/`strict` 显式开启后才改变选路。
- **状态分离**：业务配置在 `config.json`；节点、订阅、健康、冷却、延迟与事件在 `egress.state_path` 的 SQLite 中独立更新；请求日志在 `logging.requests.state_path` 的独立 SQLite，缓冲写入不阻塞请求。

模型排名引擎独立于出口调度：按「模型 + 任务类型」计算综合分，模型管理页列表顺序、路由诊断与轮转顺序都由它提供依据。

## 模型排名

排名对象是「模型 + 任务类型」。Zen 与 Go 是同一模型的两条接入路线，排名阶段合并：费用取最便宜路线，可用性与能力取最好值，成功率与延迟按渠道汇总。无上游 Key 时免费模型走匿名通道参与排名。

```
S = G × (0.40Q + 0.20E + 0.25A + 0.15F)
Q = 0.50L + 0.30B + 0.20I          # 排行榜 / 专项基准 / 内部实测
E = 100 / (1 + C / C_ref)          # 单任务有效成本
A = 0.60U + 0.25La + 0.15H         # 成功率 / P95 延迟 / 额度余量
F = 0.50M + 0.25Cx + 0.25To        # 模态 / 上下文 / Agent 能力
G ∈ {0,1}                          # 硬性准入
```

默认任务预设：`agent-coding`（40/20/25/15）、`general-text`（35/25/25/15）、`vision`（35/15/25/25）。缺失的 Q 指标按其余可用指标重新归一化；实测样本不足时降低置信度。完整定义见 `project-context/specs/model-ranking.md`。

## 管理界面

13 个页面：总览（含实时监控）、模型管理、代理节点池、代理订阅、模型轮转、上游管理、请求日志、额度看板、路由诊断、API 密钥、系统设置、接口调试、接入手册。

界面深色/浅色主题可切换，标准分辨率下各页面无需纵向滚动即可总览；完整 API 列表见 [`docs/api.md`](docs/api.md)。

## 配置

完整字段见 [`docs/configuration.md`](docs/configuration.md)，示例见 `config.example.json`。

| 配置 | 作用 |
| :--- | :--- |
| `upstream.*` | 上游地址唯一来源 |
| `retry.max_attempts` | 整个请求的唯一尝试预算 |
| `performance.failure_cooldown_seconds` | 通道层（Key）冷却 |
| `egress.cooldown` | 节点层冷却，指数退避 |
| `egress.failover` | 429/401/403/5xx 策略与 `Retry-After` |
| `egress.probe` | 探测间隔、超时、并发、目标与出口 IP 探测 |
| `egress.subscriptions` | URL 订阅源，支持 `ephemeral` 熔断驱逐；留空仍导入旧 proxies/proxyfile |
| `egress.geoip` | 国家地区库下载与刷新 |
| `ranking.*` | 任务权重、能力分输入、标准任务 Token、延迟阈值与额度余量 |

## CI 与自动更新

- `.github/workflows/checks.yml`：Ubuntu/Windows × Go 1.26.x/stable 的 vet/build 矩阵、gofmt、WebUI 构建、test/race、Chromium + Firefox E2E。
- `.github/workflows/release.yml`：`v*` 标签构建六平台产物、GHCR 多架构镜像、GitHub Release，并发布 `@ianwusb/mesh` 及平台二进制包到 npm（OIDC provenance）。
- `.github/workflows/update-dependencies.yml`：每天更新 Go 补丁依赖与前端依赖，验证后推送 `main` 并显式触发 checks。
- `.github/workflows/upstream-sync.yml`：每天同步上游 opencode2api / Resin 变更，干净改动开草稿 PR，冲突记 Issue。
- 失败邮件：仓库 Secrets 配置 `SMTP_SERVER`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`NOTIFY_EMAIL`（可选 `SMTP_PORT`、`SMTP_SECURE`、`SMTP_FROM`）。npm 发布可配置 `NPM_TOKEN`，未配置时使用 npm 平台的 GitHub Actions 可信发布。

## 升级与回滚

- 配置保存采用原子替换，旧文件保留为 `config.json.bak`。
- 升级前备份 `config.json` 与 `egress.state_path`；替换二进制后启动即可，状态库自动迁移（只向前）。
- 回滚：恢复上一版二进制与配置备份；建议同时恢复状态库备份。
- 订阅刷新与配置热重载不会中断进行中的请求。

## 开发

```bash
# 仓库根目录
go build ./...
go test ./...
go test -race ./...
go vet ./...

# 前端
cd webui && npm ci && npm run build

# WebUI E2E（Chromium + Firefox）
go build -o mesh ./cmd/mesh            # Windows: -o mesh.exe
cd webui && npx playwright install chromium firefox   # 首次
npx playwright test
```

端到端与故障注入用例位于 `internal/gateway/e2e_test.go`；WebUI 浏览器用例位于 `webui/e2e/`。npm 包装脚本位于 `npm/`，`node npm/scripts/publish-npm.mjs --tag vX.Y.Z --dist <release-files>` 可本地生成包（加 `--publish` 发布）。

## 许可证

本项目按 **GPL-3.0-or-later** 分发，完整文本见 `LICENSE`。第三方组件的版权与许可证声明见 `NOTICE`。
