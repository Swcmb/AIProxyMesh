# AIProxyMesh Web UI 全面改造实施规格

- 状态：Rev.3（两轮评审意见已并入；第 1 轮 16 项全部闭环，第 2 轮 6 项已并入，待终验）
- 依据：`project-context/specs/webui-redesign.md`（目标与验收提示词）+ 2026-10-21 阶段 0 审计
- API 权威参考：`docs/api.md`（与 `internal/admin/server.go` 路由注册一致）
- 执行模式：本规格批准后以 `/goal` 为执行引擎，任务顺序 T1→T10，每任务独立验证

## 1. 目的

在不改动后端业务能力、API 契约与配置格式的前提下，把现有 React WebUI 升级为统一的 OpenCode 风格深色开发者工具界面：视觉规范对齐 Geist，复杂管理交互对齐 Cloudscape 模式，功能完整保留并逐项验证。交付物是真实可用的前端重构 + 内嵌产物更新 + 验证与交付报告，而非静态原型。整体为单一设计语言：token 与组件库是唯一样式来源，禁止把三个参考项目的组件直接拼贴。

## 2. 现状基线（阶段 0 审计结论，2026-10-21 实测）

- 分支 `main`；工作区在本规格落地前干净（仅新增本文件）。
- 基线全绿：`go build ./...`、`go vet ./...`、`go test ./...` 全部通过；`cd webui && npm run build` 通过，产物与已提交 `internal/webui/dist` 逐字节一致（构建确定性验证）。§6.1「零回归」以此为对照基线。

| 维度 | 事实 |
|---|---|
| 技术栈 | React 19 + Vite 7 + TypeScript 5.9 + react-router v6（单文件路由 `app/routes.tsx`）+ TanStack Query v5 + recharts v3；样式为纯 CSS 变量（非 Tailwind） |
| 页面 | 14 个业务页 + 登录页，全部在 `RequireAuth > AppShell` 下；侧栏导航硬编码于 `AppShell.tsx` |
| 组件 | `ui/` 仅 7 个原语（Button/Badge/Card/Input/Select/Switch/Textarea）+ `Common.tsx`（PageHeader/StatCard/Loading/EmptyState/ErrorNotice/StatusBadge/JsonBlock）；缺 Modal/Drawer/Table/Toast/Tabs/Pagination/Skeleton |
| 主题 | 仅浅色：`index.html` 为 `<meta name="color-scheme" content="light">`（meta 标签，非 CSS 属性，不随开关切换表单控件与滚动条配色）；无 token 体系（`:root` 约 15 个变量），无间距/字号/层级/动效 token |
| API 客户端 | `lib/api.ts`：同源相对路径、`credentials: same-origin`、非 GET 自动附 `X-CSRF-Token`、401 广播 `mesh:unauthorized`、统一错误包络 `{error:{code,message}}`；CSRF 存 `sessionStorage` |
| 认证 | Cookie `mesh_session`（HttpOnly/SameSite=Strict）+ `X-CSRF-Token` + Origin 同源校验；CSP `style-src` 无 `'unsafe-inline'`——禁止生成 `<style>` 字符串与样式注入；React `style={{}}`（DOM style 属性，现网约 10 处）不受该 CSP 拦截，但新 UI 同样禁用，取色只走 class/token 变量 |
| 配置写契约 | `PUT /api/config` 全量替换且 `DisallowUnknownFields`——前端负载字段必须与 `lib/types.ts`/`config-update.ts` 完全同步，多余字段即 400 |
| 日志能力 | `/api/logs/requests` 已支持服务端 limit/before 游标与 model/channel/tier/status/keyword/outcome 等筛选；`/api/egress/nodes` 已支持分页、筛选、排序 |
| 测试 | webui 零测试；Go 对 auth/config/api-keys/rotation/rankings/debug 端点零测试；`go test ./...` 既有覆盖为回归基线 |
| 构建产物 | `internal/webui/dist` 入库（.gitignore 注释明示），CI `checks.yml` 会重建 WebUI；本地构建可复现 |
| 样式负债 | `base.css` 顶层类约 262/328 个无引用（历史 UI 遗留，口径见 T9） |
| 已知瑕疵 | `window.alert/confirm/prompt` 承担确认与密钥揭示输入（ConfigPage reveal 用 `window.prompt` 收密码）；`GET /api/egress/subscriptions/{id}` 返回整个订阅对象（含未脱敏 URL 与内联 `content` 原文） |

## 3. 范围与边界

### 3.1 范围内

- `webui/src` 全部页面、组件、样式、主题、设计 token。
- 通用交互原语补齐（Modal/Drawer/Table/Toast/Confirm/Tabs/Pagination/Skeleton 等）。
- 深浅双主题（深色为默认主视觉，浅色作为可切换变体）。
- 死样式清理：按实际使用集提取后删除 `base.css`/`mesh.css` 中未被引用的类（方法与防护见 T9）。
- `internal/webui/dist` 内嵌产物随前端构建更新。
- 文档同步为必做项：T10 更新 `README.md` 界面相关描述，或书面说明无需更新的理由。

### 3.2 范围外（硬边界）

- 不修改后端：Go 代码、代理转发、认证语义、`config.json` 格式、API 路径与字段一概不动。唯一例外：发现明确安全问题时先报告、经用户授权后才处理。
- 不迁移框架、不新增运行时依赖（现栈可实现全部目标；若过程中确有需要，先报理由与成本，经用户确认）。
- 不删除任何页面、路由、按钮、表单、配置项；功能只允许迁移与增强。
- 不引入假数据/模拟状态；后端未提供的指标隐藏或显式标注不可用。
- 默认不提交、不推送、不改写历史；全部改动停留在工作区，由用户决定提交时机（遵守 `webui-redesign.md` 禁止事项 9）。回退保障见 §10 快照机制；若用户授权分任务 WIP 提交，按任务边界执行。
- 不读取、打印、写入任何密钥到前端源码、console、URL 或 localStorage；掩码展示逻辑沿用后端 `maskSecrets` 输出。

### 3.3 约束

- CSP：禁止生成 `<style>` 字符串或向 document 注入样式；新代码一律 class + CSS 变量（React `style={{}}` 仅允许几何尺寸类动态值，颜色/字体一律走 token）。
- 与后端交互沿用 `lib/api.ts` 封装与 CSRF 语义，不新增第二套请求层。
- `PUT /api/config` 负载必须继续与 `lib/types.ts` 严格一致；保存前先 `GET /api/config` 比对基线，若远端已变化则提示重新加载，避免全量覆盖他人修改。
- 图表取色统一使用 `var(--token)` 字符串形式传入 recharts（stroke/fill 接受 CSS 变量），主题切换即时生效；禁止 `getComputedStyle` 二次取色（与 react-query 缓存并存会造成切主题后配色滞留）。注：SVG 呈现属性中的 `var()` 解析存在引擎差异，§6.6 需在 Chromium 与 Firefox 双引擎实测切色；若某引擎失败，预案为该属性改用 class 规则着色（CSP 允许）。
- 订阅详情端点返回未脱敏对象：UI 仅渲染 origin + 指纹 + 掩码 URL（用响应自带的 `masked_url`）；原始 URL 与内联 `content` 全文不渲染、不进 console、不进截图。
- 敏感信息出口管控（覆盖禁止事项 12）：截图存档前逐张复核无密码、无明文密钥、无未脱敏订阅内容；交付报告与日志摘录同样只允许掩码形态；本地测试用临时密码不写入任何提交或报告。

## 4. 技术方案

### 4.1 信息架构（旧功能 → 新页面映射）

14 个现有页面全部保留，按业务域重组侧栏分组；路由路径不变，旧 URL 直接可用。

| 侧栏分组 | 新页面归属 | 承接的旧页面与功能点 |
|---|---|---|
| 仪表盘 | 总览 Overview | OverviewPage（运行状态、指标卡、趋势图） |
| 仪表盘 | 实时监控 Monitor | MonitorPage（3s 轮询 + `/api/logs/stream` SSE 保留） |
| 资源管理 | 模型管理 Models | ModelsPage（排名、任务类型、可用性、手动恢复；列表含优先级与状态列） |
| 资源管理 | 节点池 Nodes | NodesPage（服务端分页/筛选/排序、启用/探测；**GeoIP 4 条镜像 URL 常量（`NodesPage.tsx:505-522`）逐字保留迁移，改造中不得改动**） |
| 资源管理 | 订阅 Subscriptions | SubscriptionsPage（CRUD、refresh、cleanup、预设；详情受 §3.3 脱敏约束） |
| 资源管理 | 轮转 Rotation | RotationPage（sota/sweet 顺序展示、当前切换） |
| 资源管理 | 上游 Upstreams | UpstreamsPage（走 `/api/config` PUT 契约） |
| 可观测 | 请求日志 Requests | RequestsPage（服务端筛选 + before 游标 + 详情抽屉） |
| 可观测 | 额度配额 Quotas | QuotasPage（Go 共享额度只读看板） |
| 可观测 | 路由诊断 Diagnostics | DiagnosticsPage（`/api/debug/models`、`/api/egress/diagnose`） |
| 配置 | API 密钥 Keys | APIKeysPage（创建/掩码/编辑/删除 + 用量） |
| 配置 | 系统设置 Settings | ConfigPage（配置编辑、reload、reveal、账户改密；只读项显式标注只读） |
| 高级 | 接口调试 Playground | PlaygroundPage（`/api/debug/inference`） |
| 高级 | 接入手册 Access | AccessPage（保留本地示例拼接，重排版式） |
| — | 登录 Login | LoginPage（同套 token 重构，置于布局外） |

映射纪律：实施过程中发现任何未列入上表的交互入口（菜单、按钮、快捷键、URL 参数），一律先登记进本表再继续，不允许静默丢弃。

### 4.2 设计 Token 与主题

- 新建 `styles/tokens.css`：颜色（深色基准 + `[data-theme="light"]` 覆盖）、间距（4px 基线）、字号字重层级（沿用 Manrope/Sora）、圆角、边框、阴影、层级 z-index 标度、过渡时长。
- 命名沿用 Geist 尺度语义（`--space-1..`、`--font-size-*`、`--radius-*`、`--shadow-*`），OpenCode 气质：深色低饱和表面 + 单一主色 + 细边框。
- `styles/base.css` 重写为 token 消费层，按 T9 方法删除死类后以使用集重建；`mesh.css` 合并入统一命名空间。
- 主题机制：废弃 `index.html` 的 `<meta name="color-scheme">`，改为在 `[data-theme]` 上声明 CSS `color-scheme` 属性（`dark` / `light`），使表单控件、滚动条随主题联动；`html` 根元素挂 `data-theme`，偏好存 `localStorage("mesh.theme")`，默认深色。
- 状态色规范全局唯一：ok / warning / error / cooldown / unknown 一套 Badge 语义，五类页面共用。

### 4.3 通用组件（`components/ui/`）

| 组件 | 关键行为 | 替代的旧实现 |
|---|---|---|
| Modal + ConfirmDialog | 焦点陷阱、Tab 循环、Esc 关闭、`role="dialog"` + `aria-modal`、危险操作红色主按钮 + 文案确认 | 全部 `window.alert/confirm/prompt`（ConfigPage reveal 密码输入改 Modal 内表单） |
| Drawer | 右侧滑出详情面板，请求/节点/订阅详情统一走它；键盘 Esc 关闭 | 现无，新增 |
| DataTable | 列定义、服务端分页（offset/limit 或 before 游标）、排序回调、行选中、空态/骨架、`<table>` 语义与列头 aria-sort | 各页面手写表格 |
| Toast + 通知队列 | 成功/失败/警告，`role="status"`/`role="alert"`，替代 alert 反馈 | 各页分散的 ErrorNotice 文案 |
| Tabs | URL 或本地状态同步，`role="tablist"` 键盘左右键 | 无 |
| Pagination | 与 DataTable 集成 | 无 |
| Skeleton | 表格/卡片加载态 | 现 Loading spinner 升级 |
| SearchField / FilterBar | 多条件组合，映射服务端参数 | 各页重复手写输入框 |

无障碍基线（全局组件级）：所有交互元素可见焦点环、可 Tab 达、有 `aria-label`/语义角色；颜色对比满足 WCAG AA。

所有组件仅用 token 变量，禁止页面内私有颜色。

### 4.4 数据与交互

- 请求层保持 `api.ts` + TanStack Query；为分页/游标补 `useInfiniteQuery`/带参 `queryKey` 封装，不引新库。
- 首屏优先级：Overview 聚合 `/api/monitor`、`/api/egress`、`/api/rotation` 的可用字段；每张指标卡必须带标签、单位与统计时间范围；错误率/延迟/限流仅当后端已返回对应指标时展示，否则隐藏该卡片（禁止编造）。
- 未保存变更：Settings/Upstreams/Subscriptions 等基于 `/api/config` 的表单，离开页面前脏检查确认；保存失败保留表单内容并展示错误码。
- 危险操作（删除 Key、purge 日志、cleanup 订阅、改密）必须经 ConfirmDialog，文案写明对象与后果。

## 5. 具体行动（任务分解）

每任务完成后立即验证再进入下一任务；每个任务开始前先做 §10 快照，快照 + 改动构成一个可独立回退的工作单元。任务行名（如「仪表盘三页」）仅为迭代分组顺序，侧栏归属一律以 §4.1 映射表为准（如 QuotasPage 侧栏属「可观测」、在 T4 先行改造）。

| # | 任务 | 主要文件 | 完成判据 |
|---|---|---|---|
| T1 | Token 与主题骨架 | `styles/tokens.css`（新）、`index.html`、`base.css` 变量层 | 双主题变量齐备；`[data-theme]` 挂 CSS `color-scheme` 属性且切换后表单控件/滚动条联动；构建通过 |
| T2 | 全局布局与侧栏 | `AppShell.tsx`、`routes.tsx`、`RequireAuth.tsx`、`LoginPage.tsx` | 5 分组导航 + 主题开关 + 14 路由可达；键盘可完成登录→导航 |
| T3 | 通用组件库 | `components/ui/*`（新增 8 项）、`Common.tsx` | 每组件有可渲染出口；tsc 零错误；键盘走查（Tab/Esc/方向键）+ aria 角色齐备 |
| T4 | 仪表盘三页 | `OverviewPage.tsx`、`MonitorPage.tsx`、`QuotasPage.tsx` | 数据全部来自真实端点；图表配色走 token 变量；指标卡均含标签/单位/时间范围；SSE 与轮询行为不变 |
| T5 | 资源管理五页 | `ModelsPage` `NodesPage` `SubscriptionsPage` `RotationPage` `UpstreamsPage` | 表格用 DataTable；筛选/分页/排序接服务端参数；操作按钮全部 mutate 到真实 API；Models 含优先级列；GeoIP 常量逐字保留 |
| T6 | 可观测两页 | `RequestsPage` `DiagnosticsPage` | before 游标分页；详情 Drawer 完整字段；订阅原文等敏感信息不落 UI/console/截图 |
| T7 | 配置两页 | `APIKeysPage` `ConfigPage` | reveal/确认走 Modal；脏检查 + 保存基线比对生效；只读设置项显式标注只读状态 |
| T8 | 高级两页 | `PlaygroundPage` `AccessPage` | 功能等价迁移 |
| T9 | 死样式与冗余清理 | `styles/*`、遗留组件 | 方法：脚本扫描 `webui/src` 全部 tsx/ts 的字符串字面量与 clsx 参数，提取实际使用的类名集；动态拼接类名（模板串前缀）单列白名单人工核对；口径 =「顶层类名字面量未出现在任何 src 文件」。删除前全量构建 + 逐页截图，删除后再构建 + 截图对比；`window.(alert|confirm|prompt)` grep 归零 |
| T10 | 验证与交付 | `internal/webui/dist`、`README.md`、报告 | §6/§7 全部执行并如实记录；README 界面描述更新或书面说明无需更新 |

## 6. 成功标准（验收）

逐项对照 `webui-redesign.md` §七，并附加可度量门槛：

1. `npm run build`（tsc --noEmit + vite）通过；`go build ./... && go vet ./... && go test ./...` 与 §2 基线（2026-10-21 全绿）一致零回归。
2. 14 个页面 + 登录页全部可达，逐页核对 §4.1 映射表无遗漏入口（勾记表随任务推进更新）。
3. 全仓库 grep：`window.alert`、`window.confirm`、`window.prompt` 结果为 0。
4. 无硬编码假数据：新增/改动代码中不存在伪造的状态、延迟、健康值（人工复查 + grep 复核）。
5. `PUT /api/config` 负载字段与 `lib/types.ts` 一致（diff 审查）。
6. 深色主题下五类页面 + 浅色主题下抽查两页可读性；常见桌面分辨率与窄窗口无溢出；recharts 图表在 Chromium 与 Firefox 双引擎下随主题切换即时变色。
7. 浏览器实测（§7 通路）：登录、导航、每页加载/空态/错误态、一次 Key 创建与删除、一次节点筛选、一次日志游标翻页、一次危险操作确认流、一次配置保存（`PUT /api/config` 全量替换）与 reload、一次订阅 CRUD（创建/刷新/删除）；控制台零错误、无 404/401 之外的无效请求。
8. 键盘操作走查：仅用键盘完成「登录 → 侧栏导航 → 打开一个详情 Drawer → 一次危险操作确认」全程无死角；焦点可见。
9. 敏感信息零出口：密钥、订阅原文/`content`、Authorization 头不出现在前端源码、列表视图、console、截图与报告中（截图逐张复核）。
10. 每个任务的 §10 快照存在且回退路径演练过一次（至少对一个页面做快照恢复 + 重放验证）。
11. 交付报告按 `webui-redesign.md` §八 8 节格式输出，未执行项如实标注。

## 7. 验证方法

分工：CI（GitHub）承载全量构建与 Go 测试回归；本规格默认不提交，因此实际验收通路为「每任务本地构建 + T10 全量浏览器实测」，两者都不可省。

本地实测通路（已验证的仓库事实）：

1. 后端：`cp config.example.json config.local.json`，把 `webui.password` 改为仅本机使用的临时密码（该值不进报告、不进提交），并把 `webui.listen` 从示例的 `0.0.0.0:8081` 改为 `127.0.0.1:8081`（示例默认绑全网卡，与「仅本机」矛盾）；`config.local.json` 保持未跟踪，`go build -o mesh ./cmd/mesh && ./mesh -config config.local.json`——内嵌产物即当前 dist，可直接验证生产形态。
2. 前端开发验证：`cd webui && npm run dev` → `http://127.0.0.1:5173`，Vite 将 `/api` 代理到 `127.0.0.1:8081`（`webui/vite.config.ts`）。改样式的迭代用 dev 通路，产物验收用 8081 通路。
3. 浏览器自动化：用会话内浏览器工具打开上述地址逐页验证并截图存档（截图过 §3.3 复核）。
4. 视觉/交互验证不能以「编译通过」替代。
5. e2e 测试框架（Playwright）不在本规格范围：属新增 dev 依赖与 CI 改动，若实施中发现回归风险过高，另立提案征求用户意见。

## 8. 迭代策略

- 单循环：快照 → 实现 → `npm run build` → 浏览器抽验本页 → 更新 §4.1 映射表勾记 → 下一任务。
- 每任务改动保持独立可回退（快照机制 §10；如用户授权可分任务提交，默认不提交）。
- 同一页面返工上限 2 轮，超限按固定模板记录后继续推进：「任务号 / 现象 / 已试方案 / 原因 / 影响 / 后续方案」，并同步进最终报告「剩余问题」节（承接禁止事项 11）。

## 9. 阻塞与停止条件

遇到以下情况暂停并请示用户，其余依代码与测试自行判断：

1. 任何需要后端 API 变更才能达成视觉/数据目标的点。
2. 发现需要覆盖用户未提交修改、删除文件、提交/推送等不可逆或越权动作。
3. 新增运行时依赖的必要性判断。
4. 同一任务连续 2 轮返工仍不收敛。
5. 发现明确安全缺陷（如脱敏遗漏）且修复超出前端范围。

## 10. 风险与预案（Contingency）

回退保障（与「默认不提交」并存，解决无检查点问题）：

- 每个任务开始前，把该任务将触碰的文件原样复制到 `project-context/artifacts/redesign-backup/T<n>/`（保持相对路径），并在任务完成判据中登记快照位置。
- 回退 = 从快照目录整文件拷回；`internal/webui/dist` 任何时候可由 `npm run build` 重建，无需快照。
- 快照目录启动时在 `project-context/README.md` 登记用途；T10 验收通过后其处置（保留或删除）列入交付报告「剩余问题」由用户裁决。
- 用户若授权分任务 WIP 提交，则以提交为快照、本节机制退化为双保险。

| 风险 | 影响 | 预案 |
|---|---|---|
| base.css 大规模重写引发隐性样式回归 | 页面局部破损 | T9 使用集提取 + 删前删后截图对比；逐任务浏览器抽验；快照整文件回退 |
| recharts 主题化取色方式不当 | 切主题后图表配色滞留 | 统一 `var(--token)` 字符串（§3.3），禁止 JS 二次取色 |
| config 全量替换并发覆盖 | 用户/其他页签配置丢失 | §3.3 保存基线比对 + 脏检查 |
| 默认不提交导致改动丢失（断电/误 restore） | 工作损失 | §10 任务快照 + 每任务边界向用户汇报进度，由用户决定何时授权提交 |
| 深色主题工作量超预期 | 超期 | 浅色作为 token 覆盖层成本低，主视觉深色优先；如需砍范围先请示（砍深色/浅色之一需批准） |
| 浏览器实测依赖本地登录态 | 验证中断 | 用 §7 临时密码流程；若被限速（5 次/5 分钟）按冷却等待，不绕过认证 |

## 11. Goal 字段（/goal 执行引擎输入）

- **Goal**：按 §5 完成 T1→T10，达成 §6 全部验收项，交付 `webui-redesign.md` §八 格式报告（与 §6.11 同一引用）。
- **Boundaries/Constraints**：§3.2 范围外与 §3.3 约束全程有效。
- **Verification Method**：§7。
- **Iteration Strategy**：§8。
- **Blocking/Stop Conditions**：§9 命中即停。
