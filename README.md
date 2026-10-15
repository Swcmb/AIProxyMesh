# AIProxyMesh

把 Resin 代理池与 opencode2api 多实例串成一条链路的部署编排仓库：

```
客户端 → New API ─┬─ oc2api-1 (代理组 a) ─┐
                  ├─ oc2api-2 (代理组 b) ─┤
                  ├─ oc2api-3 (代理组 c) ─┼→ Resin 代理池 → opencode.ai/zen
                  └─ oc2api-4 (代理组 d) ─┘
```

Resin 维护上游代理池并提供统一的 HTTP/SOCKS5 入口，每个 opencode2api 实例绑定一组代理
（各 40 个）访问 OpenCode Zen，New API 作为统一网关对客户端暴露模型。

## 仓库结构

| 路径 | 内容 |
|:---|:---|
| `Resin/` | 子模块，上游 [Resinat/Resin](https://github.com/Resinat/Resin)（MIT） |
| `opencode2api/` | 子模块，上游 [jasonxu114514/opencode2api](https://github.com/jasonxu114514/opencode2api)（GPL-3.0） |
| `deploy/resin/` | Resin 的 compose 与 `.env` |
| `deploy/opencode2api/` | 4 实例 compose、配置生成脚本、代理清单 |

克隆时一并拉取子模块：

```bash
git clone --recurse-submodules <repo-url>
```

## 凭据

真实凭据不入库，clone 后按下表补齐：

| 文件 | 来源 |
|:---|:---|
| `deploy/resin/.env` | 复制 `deploy/resin/.env.example`，替换两个 token |
| `deploy/opencode2api/instance-N/config.json` | 由 `generate_configs.py` 生成 |
| `deploy/opencode2api/credentials.json` | 同上，脚本汇总的 server_key 与 WebUI 口令 |

```bash
# 生成 4 个实例的 config.json 与凭据汇总（server_key、口令随机生成）
python3 deploy/opencode2api/generate_configs.py deploy/opencode2api
```

## 部署

```bash
# Resin：管理控制台与代理入口在 2260
cd deploy/resin && docker compose up -d

# opencode2api：4 实例，API 127.0.0.1:8080/8082/8084/8086，WebUI 8081/8083/8085/8087
cd deploy/opencode2api && docker compose up -d
```

`deploy/opencode2api/docker-compose.yml` 复用外部的 `new-api_default` 网络，需先有
New API 容器；`proxies/` 以只读方式挂载进各实例。
