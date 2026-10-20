# 在线真实凭据冒烟报告

| 项 | 值 |
| :--- | :--- |
| 日期 | 2026-10-20 |
| 版本 | `v0.1.2`（Windows amd64 二进制） |
| 上游 | `https://opencode.ai/zen`（真实网络） |
| 凭据 | 本机 OpenCode 已登录账户的 OAuth 会话令牌（不落库、不打印、不提交） |
| 出口 | `direct`（旧 `proxies` 兼容导入） |
| 配置 | 临时目录，启动后已删除；WebUI 明文密码已自动哈希 |

## 1. 就绪与模型发现

| 检查 | 结果 |
| :--- | :--- |
| `/healthz` | `status=ok`、`ready=true`、`version=v0.1.2` |
| 模型目录 | `total=66`（Zen 44 + Go 37 去重）、`exposed=35`、`cache_source=live` |
| `/v1/models` | 200，35 个模型，含 8 个 free 模型 |
| 元数据 | models.dev 141 个模型 |
| Key / 旧代理 | Zen 1 / Go 0；proxies 1（direct） |

## 2. 真实推理

| 用例 | 结果 |
| :--- | :--- |
| `POST /v1/chat/completions`（`deepseek-v4.1-flash-free`，非流式） | 200，内容 `OK`，用时约 2.5s，usage 1595 tokens（prompt 1593 / completion 2） |
| 同一请求 `stream=true` | 200 `text/event-stream`，包含 `data:` 帧与 `[DONE]`，用时约 5.6s |
| 出口归因 | 两次请求均 `attempts=1`、`success=true`、`exit_node=direct`、`proxy_node=egress` |
| 监控 | 累计 3 次请求、成功率 100%、3190 tokens |
| 切换事件 | 2 条 `success / 200 / direct / ok` |

## 3. 出口池与管理面

| 检查 | 结果 |
| :--- | :--- |
| `/api/egress` | 启用；1 个 direct 节点，eligible=1，冷却/停用/不支持均为 0 |
| 节点指标 | 健康度 0.75、延迟约 1907ms（启动探测） |
| 手动探测 | 200；延迟约 1885ms、健康度 0.80 |
| `/api/model-quotas` | 200；Go 账号 0（本机无 Go 订阅）、Go 可用模型 15；返回共享套餐说明 |
| `/api/models/availability` | 9 个免费模型，禁用 0 |
| 管理登录 / CSRF | 200，Session + CSRF 正常 |

## 4. 安全与清理

- 服务日志中未出现 OAuth 令牌或本地 API Key（脚本检查 `False`）。
- 配置文件中的 WebUI 明文密码已移除，写入 `password_hash`。
- 临时配置、日志、SQLite 状态与 `secrets.env` 已整体删除。
- 仓库工作区保持干净；未提交任何凭据。

## 5. 结论

真实凭证下，模型发现、Zen Key 路由、统一出口选择、JSON 与 SSE 推理、管理面、额度接口与观测链路均正常。Go 套餐因本机账户未订阅，仅验证到接口返回 200 与空账号列表；Go 推理需在有订阅的账户下复验。
