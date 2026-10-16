# AGENTS.md

本仓库是一个**聚合/部署仓库**，自身没有源码与构建系统——代码全部以 git 子模块形式存在：

- `Resin/`：Go 代理池网关（子模块，分支 `master`，上游 https://github.com/Resinat/Resin）。将大量代理订阅聚合为带会话粘性的统一代理入口，含 HTTP/SOCKS5 正向、HTTP 反向代理与 WebUI。
- `opencode2api/`：Go 网关（子模块，分支 `main`，上游 https://github.com/jasonxu114514/opencode2api）。对外暴露 Chat Completions / Responses / Anthropic Messages 三种协议并做协议转换，管理上游 key 与代理，可执行文件内嵌 WebUI。

## 构建与测试（均在子模块目录内执行，Go ≥ 1.24）

```bash
cd Resin && go build ./... && go test ./...
cd opencode2api && go build -o opencode2api ./cmd/opencode2api && go test ./...
```

根目录没有 Makefile / 脚本，`go` 命令在根目录运行无效。

## 注意事项

- 子模块是**上游 fork 的只读镜像**：修改应在上游仓库进行，本仓库只通过 `git submodule update --remote` 跟进；不要在子模块内提交本地改动后推送。
- 克隆后子模块默认为空，需 `git submodule update --init`。
- 历史上存在的 `deploy/`（docker-compose、配置生成脚本）与 README 已在本分支移除；如需恢复参考 git 历史（HEAD 前几次提交）。
- 两个项目均把配置文件（`config.json`、`.env` 等）视为敏感内容，已被 `.gitignore` 排除，不要提交真实密钥。
- `Resin/DESIGN.md` 与 `doc/`、`opencode2api/docs/` 是理解各自架构的入口。

## 维护规则

当项目结构、构建测试命令、架构边界、子模块版本或开发约定发生变化时，必须在同一次改动中同步更新本文件。
