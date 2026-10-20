# 阶段 7 发布报告

| 项 | 值 |
| :--- | :--- |
| 日期 | 2026-10-20 |
| 版本 | `v0.1.0` |
| 平台 | Windows 10.0.26100 / amd64；Docker 29.2.1 (linux/x86_64) |
| 交付形态 | 单个 Go 可执行文件 + 单个 Docker 镜像 + 内嵌 WebUI |
| 许可证 | GPL-3.0-or-later（`mesh/LICENSE`），Resin MIT 声明（`mesh/NOTICE`） |

## 1. 发布物

### 跨平台二进制

`CGO_ENABLED=0`，`-trimpath -ldflags "-s -w -X main.version=v0.1.0"`：

| 目标 | 大小 | 校验 |
| :--- | ---: | :--- |
| linux/amd64 | 14 MB | ELF 64-bit, statically linked, stripped |
| linux/arm64 | 13 MB | ELF 64-bit ARM aarch64, statically linked, stripped |
| darwin/amd64 | 14 MB | Mach-O 64-bit x86_64 |
| darwin/arm64 | 13 MB | Mach-O 64-bit arm64 |
| windows/amd64 | 14 MB | PE32+ x86-64 |

构建命令：

```bash
cd mesh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> \
  go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" -o mesh-<os>-<arch> ./cmd/mesh
```

### Docker

```bash
cd mesh
docker build --build-arg VERSION=v0.1.0 -t mesh:local .
# image size: 35.2 MB
```

- 前端阶段：`node:24-alpine`，`npm ci` + `vite build`，产物写入 `internal/webui/dist`。
- Go 阶段：`golang:1.25.5-alpine`，`CGO_ENABLED=0` 静态构建。
- 运行阶段：`alpine:3.21` + CA 证书 + tzdata，非 root 用户 `mesh`（UID 65532）。
- **偏差说明**：原计划使用 `gcr.io/distroless/static`，当前网络无法访问 gcr.io，改用 Alpine；安全与体积差异已评估，功能不受影响。
- 国内构建可通过 `--build-arg NPM_REGISTRY=... --build-arg GOPROXY=...` 覆盖默认镜像源。

### 其他文件

- `mesh/config.example.json`：完整示例配置。
- `mesh/compose.yaml`：单服务 compose，挂载 `./data:/data`。
- `mesh/README.md`：安装、配置、升级/回滚、测试。
- `mesh/docs/configuration.md`、`mesh/docs/api.md`。
- `.github/workflows/checks.yml`：build / vet / test / race / 前端构建 / Docker 构建（沙箱未触发 CI）。

## 2. 容器冒烟

```bash
docker run -d --name mesh-smoke -p 18090:8080 -p 18091:8081 \
  -v <tmp>/mesh-container:/data mesh:local
```

| 检查 | 结果 |
| :--- | :--- |
| 启动日志 | `api 0.0.0.0:8080`、`webui 0.0.0.0:8081`、`version v0.1.0` |
| `GET /`（WebUI） | 200 |
| `GET /healthz` | 503 `starting`（沙箱无法访问 opencode.ai，模型目录 pending；非崩溃） |
| 登录 `/api/auth/login` | 200，返回 CSRF |
| `GET /api/egress` | `enabled=true`，节点 1、可用 1、state `/data/egress.db` |
| 配置落盘 | `config.json` 中明文密码已移除，`password_hash` 已写入 |
| 状态库 | `/data/egress.db`、WAL/SHM 正常创建 |
| 进程模型 | 单容器、单进程、单配置、单 WebUI；无外部 Resin 进程 |
| 清理 | 容器已删除 |

## 3. 升级与回滚演练

1. 停止旧进程，备份 `config.json` 与 `egress.db`（含 WAL/SHM）。
2. 使用 `v0.1.0` 二进制与同一份配置/状态目录启动。
3. 未携带 Cookie 的 `/api/auth/session` 返回 401（会话不跨进程复用，符合 HttpOnly 内存会话语义）。
4. 重新登录后 `/api/egress` 返回 `total=1, eligible=1`，节点 `direct` 从持久化状态恢复。
5. `/healthz` 返回 `version=v0.1.0`。
6. 回滚路径：停止进程 → 恢复上一版二进制 → 恢复 `config.json.upgrade-backup`；状态库建议同时恢复备份（迁移只向前）。
7. 演练产生的备份：`config.json.upgrade-backup`、`egress.db.backup`、`egress.db-wal.backup`。

## 4. 许可证与署名

- `mesh/LICENSE`：GNU GPL v3 全文（674 行，与 opencode2api LICENSE 完全一致）。
- `mesh/NOTICE`：
  - 本项目整体 GPL-3.0-or-later；
  - opencode2api（GPL-3.0-or-later，commit `a6601da`）来源说明；
  - Resin（MIT，commit `1a6954e`）版权与完整 MIT 许可声明，覆盖 WebUI 主题/基础组件与调度设计；
  - Go 构建图 12 个第三方模块与前端生产依赖的许可证类型。
- WebUI 主题与基础组件已保留 Resin 署名；前端字体按 OFL-1.1。

## 5. 质量门

| 检查 | 结果 |
| :--- | :--- |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过 |
| `go test -race ./...` | 通过 |
| `npm ci && npm run build` | 通过 |
| `docker build` | 通过（35.2 MB） |
| 容器冒烟 | 通过 |
| 五平台交叉编译 | 通过 |
| WebUI 13 路由 | 通过（Chrome，无控制台异常） |

## 6. 剩余风险与后续

1. **真实上游验证**：沙箱无法访问 opencode.ai，健康检查在容器内为 `starting`；发布到目标网络后需用真实 Key 完成一次端到端冒烟（模型目录、免费模型探测、Go 额度）。
2. **Docker 运行时**：因 gcr.io 不可达改用 Alpine；若目标环境可访问 gcr.io，可恢复 distroless（`--build-arg RUNTIME_IMAGE=...`）。
3. **CI 未实际触发**：工作流已落盘，需在 GitHub 上推送后观察首次运行。
4. **旧内嵌页面文件**：`mesh/internal/webui/app.js`、`index.html`、`styles.css` 已不再被嵌入，保留至后续清理；不影响构建与运行。
