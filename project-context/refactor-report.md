# 重构记录：自包含仓库与排名驱动轮转

日期：2026-10-20。分支：`refactor/self-contained-ranking`。

## 一、仓库自包含化

- 移除 `Resin/`、`opencode2api/` 两个子模块与 `.gitmodules`；`mesh/` 成为唯一源码目录，根目录不再有任何构建入口。
- 清除代码、文档、CI 与前端中对外部项目名的显式引用：会话 Cookie 改为 `mesh_session`，标识符与注释改为通用描述（`mesh:default-project`、`mesh-admin/1.0`），README/AGENTS 重写为单产品说明。
- 唯一保留的第三方署名在 `mesh/NOTICE`：派生代码的 GPL-3.0 与 MIT 许可要求逐字保留版权声明与许可原文，属于法定义务，与“仓库正文不提项目名”并不冲突（该文件只承担许可合规，不作为产品说明）。
- 旧的集成期文档（需求、功能矩阵、回归/冒烟/发布报告）从版本库移除，历史保存在本地临时归档目录。
- `.gitignore` 重写为只忽略本地配置、状态库、依赖目录与构建产物；内嵌前端产物 `mesh/internal/webui/dist` 保持入库。

## 二、自动更新能力保留

- 删除 `update-submodules.yml`（其职责是推进外部提交指针，已无对象）。
- 新增 `.github/workflows/update-dependencies.yml`：每日定时对 Go 依赖做补丁级更新（`go get -u=patch` + `go mod tidy`），对前端依赖做范围内的 `npm update`，重建内嵌产物，先跑 `go vet` / `go test` 验证，有变化才提交并推送 `main`，随后显式触发 Checks 工作流；失败调用 `notify-failure.yml` 发送邮件。
- Checks、Release、Notify-failure 三个工作流保持原有结构不变。

## 三、模型排序改为综合评分

排序依据 `specs/model-ranking.md`：`S(r,t) = G(r,t) × [0.40Q + 0.20E + 0.25A + 0.15F]`（Agent 编程预设）。

新增 `mesh/internal/ranking`：

- 排名对象是「提供商 + 模型 + 任务类型」，同名模型在 Zen 与 Go 上分列两条记录。
- `G` 为准入开关：可路由性、工具调用、图像输入、最小上下文；未通过者保留展示但不参与排序，得分为 0。
- `Q` 由排行榜 / 专项基准 / 内部实测按 50%/30%/20% 加权，取值转换为同批候选内的百分位；缺失项按剩余权重重新归一化。
- `E = 100 / (1 + C_r / C_ref)`，`C_ref` 为同任务合格路线的单任务成本中位数；免费路线为 100 分。
- `A = 0.60U + 0.25L_a + 0.15H`，成功率与 P95 延迟来自最近 24 小时运行数据，额度余量来自配置。
- `F = 0.50M + 0.25C_x + 0.25T_o`，模态、上下文/输出长度、Agent 能力按任务目标值归一。
- 无数据维度按中性 50 分计入综合分，同时降低 `confidence`；`sample_count` 与置信度一并输出，避免少量样本冲顶。

接入点：

- `internal/gateway/ranking.go` 由模型目录、发布价格、订阅分摊与运行采样构造路线记录。
- `syncRotation` 改为按任务预设生成 `sota` / `sweet` 的顺序，调用新的 `rotation.Manager.Apply`；当前模型只要仍在合格列表且可用就保持不变，被移出或不可用时切到新顺序的第一位，并记录“综合排名更新”切换原因。
- 新增只读接口 `GET /api/rankings?task=` 与 `GET /api/rankings/tasks`，沿用 session + CSRF 认证。
- WebUI 增加「模型排名」页（任务切换、准入说明、四维度得分、成本/延迟/样本/置信度），轮转页改为展示排名顺序并支持手动切换。

## 四、破坏性变更

- 配置：`rotation.sota.order`、`rotation.sweet.order` 与内置的固定 SOTA 模型清单删除；旧配置文件保留这些字段会因严格校验而启动失败，需要删除后重启。
- 配置新增可选 `ranking` 段，全部字段有默认值。
- `PUT /api/rotation` 只接受别名与 `first_output_seconds`。

## 五、验证

本地：`go vet ./...`、`go test ./...`、`go test -race ./...` 全绿；新增 `internal/ranking`（7 个用例，覆盖门槛、百分位归一化、成本中位数、延迟截断、适配度、Go/Zen 分列）、`internal/rotation`（顺序刷新、粘滞、去重与不可用剔除）、`internal/config`（别名/排名校验、默认值、Clone 隔离）与 `internal/gateway` 排名集成用例；`npm run build` 通过并刷新内嵌产物。GitHub Actions 的 Checks / Update dependencies 结果以 PR 工作流运行为准。
