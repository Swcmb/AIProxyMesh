# 上游同步、自动发版与代理池预设

> 状态：已确认设计，待实施
> 适用仓库：`Swcmb/AIProxyMesh`
> 日期：2026-10-21

## 一、背景与目标

本仓库已自包含重构，与两个参考上游没有共同历史：

| 上游 | 地址 | 分支 | 许可 | 发布节奏 |
| :--- | :--- | :--- | :--- | :--- |
| opencode2api | `jasonxu114514/opencode2api` | `main` | GPL-3.0 | 近乎每日（v1.4.x） |
| Resin | `Resinat/Resin` | `master` | MIT | 数月一次（最新 v1.2.0） |

目标：

1. 上游有新提交时，自动尝试把补丁机械移植进本仓库，并把无法自动完成的部分写成带专用标签的 Issue。
2. 同步结果合入 main 后，按保守版本规则自动构建并发布新 Release。
3. 同步冲突、检查失败、发版失败等异常通过 GitHub Issue 通知，不依赖邮件。
4. 把 `Swcmb/api-proxy-pool` 产出的公开代理池作为内置预设放进订阅页，默认不启用。

范围外：前端功能与 opencode2api / Resin 的逐页对齐另立规格，不在本文档内。

## 二、总体架构

```
schedule / dispatch
        │
        ▼
upstream-sync.yml ──逐文件 git apply --3way──► 草稿 PR (upstream-sync, sync:patch|minor)
        │                                              │
        ├── 冲突/缺基线 ──► Issue (upstream-sync)       │ merged
        └── 无改动/无可移植 ──► 仅更新状态              ▼
                                                version-bump.yml
                                                       │ 升版号 + tag + 显式分发
                                                       ▼
                                                release.yml / checks.yml
```

两条工作流：

- `.github/workflows/upstream-sync.yml`：每日 04:17 UTC + `workflow_dispatch`。
- `.github/workflows/version-bump.yml`：监听 `pull_request` 的 `closed` 事件。

共享脚本：`.github/scripts/upstream-sync.sh`（bash，仅 ubuntu runner 执行）。YAML 只负责编排与权限。

## 三、状态文件

`project-context/upstream-state.json`，唯一事实来源：

```json
{
  "schema": 1,
  "upstreams": {
    "opencode2api": {
      "repo": "jasonxu114514/opencode2api",
      "branch": "main",
      "synced_sha": "<已合入的提交>",
      "blocked_sha": "<已开 Issue 待人工处理的提交>",
      "blocked_issue": 0,
      "last_release": "v1.4.3",
      "last_checked": "2026-10-21T04:17:00Z"
    },
    "resin": {
      "repo": "Resinat/Resin",
      "branch": "master",
      "synced_sha": "<已合入的提交>",
      "blocked_sha": "",
      "blocked_issue": 0,
      "last_release": "v1.2.0",
      "last_checked": "2026-10-21T04:17:00Z"
    }
  }
}
```

规则：

- `synced_sha` 只在同步结果合入 main 后推进（由 `version-bump.yml` 写入）。
- `blocked_sha` / `blocked_issue` 在冲突 Issue 打开时写入；Issue 关闭后，下一次同步把 `synced_sha` 推进到 `blocked_sha` 并清空 blocked 字段（契约：关闭 Issue 表示该区间已人工处理完）。
- 状态写入一律用 `git pull --rebase` 后直接提交 main；这类提交不触发工作流（`GITHUB_TOKEN` 推送），属预期。
- `dry_run` 不修改状态。

## 四、同步流程

### 4.1 触发与并发

```yaml
on:
  schedule: [{ cron: "17 4 * * *" }]
  workflow_dispatch:
    inputs:
      upstream: { type: choice, options: [all, opencode2api, resin], default: all }
      dry_run:  { type: boolean, default: false }
permissions:
  contents: write
  pull-requests: write
  issues: write
  actions: write
concurrency: { group: upstream-sync, cancel-in-progress: false }
```

### 4.2 单个上游的处理

1. 读取状态：
   - `blocked_sha` 非空且 `blocked_issue` 仍为 open → 跳过该上游。
   - `blocked_sha` 非空且 `blocked_issue` 已关闭 → 令 `synced_sha = blocked_sha`、清空 blocked 字段，提交状态后继续。
2. 若已存在未关闭的、标签为 `upstream-sync` 且正文含 `<!-- upstream-sync: <name> -->` 的 PR，跳过。
3. `git fetch https://github.com/<repo>.git <branch>`，取 `FETCH_HEAD`。必须取回完整 blob（不要 `--filter=blob:none`），否则 `git apply --3way` 缺少前像而失败。
4. 若 `FETCH_HEAD == synced_sha`：更新 `last_release`（若上游有更新），结束。
5. 取 `git log --reverse --format=%H synced_sha..FETCH_HEAD` 的提交列表，从 main 建分支 `sync/<name>-<to_sha 前 8 位>`。
6. 逐提交、逐文件尝试移植（见 4.3）。
7. 生成报告 `project-context/upstream-sync/<name>-<from8>-<to8>.md`：提交清单、每个文件的 `applied / conflict / missing-base`、区间内的上游 Release。
8. 分支结果：
   - 有 `applied`：提交报告与改动，push，创建**草稿** PR；标签 `upstream-sync` 与 `sync:patch` 或 `sync:minor`；正文首行放机器可读标记（`blocked` 表示该区间仍有未处理冲突）：
     `<!-- upstream-sync: opencode2api <from_sha> <to_sha> <bump> blocked=0|1 -->`
   - 存在 `conflict` / `missing-base`：无论是否开了 PR，都创建或更新冲突 Issue，并在 main 写 `blocked_sha = to_sha`、`blocked_issue = <编号>`。PR 只承载可以干净应用的那部分改动。
   - 只有 `applied`、没有冲突：直接走 PR，不写 blocked 状态。
9. 每次运行顺带核对所有未关闭的 `upstream-sync` 草稿 PR：若其 Checks 失败，创建或更新「检查失败」Issue。
10. `dry_run=true`：只生成报告并作为 artifact 上传，不建分支、不开 PR/Issue、不写状态。

### 4.3 逐文件移植规则

对每个提交的每个改动文件执行：

```bash
git diff <sha>^ <sha> -- <path> | git apply --3way --whitespace=nowarn --index
```

分类：

| 结果 | 判定 | 处理 |
| :--- | :--- | :--- |
| `applied` | 命令成功 | `git add`，计入 PR |
| `conflict` | 命令失败且本仓库存在该路径 | 记入报告，留待 agent 重写 |
| `missing-base` | 命令失败且本仓库不存在该路径，且上游该提交不是新增文件 | 记入报告 |

路径映射与保护：

- 上游 `internal/`、`cmd/`、`webui/`、`docs/`、`scripts/` 映射到本仓库的 `mesh/` 同名目录（`git apply --directory=mesh`）；其余路径原样。
- 只对本仓库已跟踪的文件做就地补丁。上游新增文件一律记为 `missing-base`，避免把另一套前端/脚本当死代码塞进来。
- `.github/**`、`project-context/**`、`AGENTS.md`、`CLAUDE.md`、许可证与 NOTICE、`mesh/go.mod`、`mesh/go.sum`、Dockerfile、compose、`.gitignore`、`.gitattributes` 受保护，永不自动改写。

约束：

- 不修改上游文件中的版权头与 `SPDX-License-Identifier`；新增第三方文件需同步登记到 `mesh/NOTICE`（在报告的检查清单中提示）。
- 二进制文件跳过自动移植，直接记为 `conflict`。
- 单个文件失败不影响其余文件与提交。

### 4.4 合入后的发版

`version-bump.yml`：

1. 触发条件：`pull_request.closed == true`、`merged == true`、PR 含 `upstream-sync` 标签。
2. 从 PR 正文解析 `<!-- upstream-sync: <name> <from> <to> <bump> blocked=0|1 -->`；解析失败按 `patch` 处理并开 Issue。
3. 版本计算：取最新 `v*` 标签，按 `bump` 递增；若 `bump=minor` 且上游为 MAJOR，仍按 MINOR（MAJOR 只能人工打）。
4. 更新状态：
   - `blocked=0`：`synced_sha = <to>`、清空 blocked 字段、`last_release` 取区间内最新上游 Release，提交 `chore(release): vX.Y.Z` 并推 main。
   - `blocked=1`：只提交 `last_release`；`synced_sha` 保持不动，等待冲突 Issue 关闭后由同步流水线推进。
5. 打 tag `vX.Y.Z` 并推送（留档）。
6. **显式分发**：`gh workflow run release.yml -f tag=vX.Y.Z` 与 `gh workflow run checks.yml`。原因：`GITHUB_TOKEN` 推送的 tag/branch 不会触发 `push` 事件工作流。
7. 失败时创建 `release-failed` Issue。

权限：`contents: write`、`pull-requests: write`、`issues: write`、`actions: write`。

### 4.5 Issue 通知

专用标签：`upstream-sync`（颜色 `FBCA04`，描述「上游同步与兼容性通知」），工作流启动时用 `gh label create --force` 保证存在。

| 类型 | 标题（固定，区间写正文） | 触发 |
| :--- | :--- | :--- |
| 冲突需人工 | `[sync] <name> 同步冲突` | 存在 conflict / missing-base |
| 检查失败 | `[sync] <name> 同步 PR 检查失败` | 草稿 PR 的 Checks 失败 |
| 无可移植 | `[sync] <name> 新 Release 无可移植变更` | 有新 Release 但提交无法应用 |
| 发布失败 | `[release] 发布失败` | version-bump 的 tag/分发失败 |

去重：按「固定标题 + `state=open`」搜索；命中则追加评论（运行链接、区间、新增提交），不新开。

## 五、版本号规则（保守映射）

| 上游变化 | 本项目 |
| :--- | :--- |
| PATCH | PATCH |
| MINOR | MINOR |
| MAJOR | MINOR（人工评估后才可升 MAJOR） |
| 无 Release，含 `feat:` 提交 | MINOR |
| 无 Release，其余提交 | PATCH |

发布仍由 `v*` 标签触发；本规则只决定同步合入时打哪个标签。

## 六、代理池预设

- 后端新增 `GET /api/egress/subscription-presets`（需登录），返回内置预设列表；唯一预设：

  ```json
  {
    "id": "api-proxy-pool",
    "name": "api-proxy-pool 公共代理池",
    "url": "https://swcmb.github.io/api-proxy-pool/alive.txt",
    "format": "uri",
    "description": "GitHub Actions 每日筛选的公开 HTTP/SOCKS 代理列表",
    "risk": "公共代理，可用性与隐私风险高于自建出口；启用前请确认可接受"
  }
  ```

- 订阅页新增「预设」卡片：展示名称、来源、风险提示与「添加」按钮。添加走现有 `POST /api/egress/subscriptions`（id `api-proxy-pool`、`refresh_seconds: 3600`、`enabled: true`）。
- **默认不启用**：不写入 `config.json`，不在启动时自动添加；只有用户点击添加后才成为订阅。
- 部分启用：添加后在节点池里按节点开关单独停用；订阅级开关可整体停用。
- 订阅状态中的 URL 继续只显示 origin，完整地址仅在单订阅详情可见。

## 七、测试与验收

1. `workflow_dispatch` + `dry_run=true`：产出报告 artifact，仓库无任何写入。
2. 首次真实运行：opencode2api 前进了 5 天，预期产生大量 `conflict` / `missing-base`，生成冲突 Issue 与 blocked 状态；报告内容与上游提交一一对应。
3. 构造一次可干净应用的上游改动（如新增独立文档），确认草稿 PR 标签、正文机器标记、状态推进与发版链路。
4. 关闭冲突 Issue 后再次运行：`synced_sha` 推进到 `blocked_sha`，blocked 字段清空。
5. 合并一次同步 PR：确认 `chore(release)` 提交、tag、`release.yml` 与 `checks.yml` 被显式分发，Release 产物齐全。
6. 代理池预设：订阅页出现卡片；点击添加后节点池出现该源节点；默认配置不含该订阅。

## 八、风险与限制

- 机械移植只能覆盖上下文完全一致的改动；多数上游源码会被判为冲突，语义对齐仍由 agent 按报告重写。
- 两个上游每日检查一次，最多产生 1 个草稿 PR 与 1 个滚动 Issue/上游，不会刷屏。
- `GITHUB_TOKEN` 推送不触发工作流，所有自动发版必须显式 `gh workflow run`。
- 公开代理池的流量会经过第三方主机：仅建议在可接受该风险时启用，且不要把它作为唯一出口。
- 自动应用的第三方文件必须保留上游许可证头，并在 `mesh/NOTICE` 登记。

## 九、实现清单

| 文件 | 作用 |
| :--- | :--- |
| `.github/scripts/upstream-sync.sh` | 拉取、逐文件应用、报告、PR/Issue、状态写入 |
| `.github/workflows/upstream-sync.yml` | 调度、权限、并发、artifact |
| `.github/workflows/version-bump.yml` | 合入后升版、tag、显式分发 |
| `project-context/upstream-state.json` | 同步状态 |
| `mesh/internal/admin/subscriptions.go` | 预设端点 |
| `mesh/webui/src/pages/SubscriptionsPage.tsx` | 预设卡片 |
| `mesh/docs/configuration.md` / `mesh/docs/api.md` | 文档同步 |
| `AGENTS.md` | 自动化边界同步 |
