# AIProxyMesh Web UI 全面升级交付报告

- 日期：2026-10-21
- 依据：`project-context/specs/webui-redesign.md`、`project-context/specs/webui-redesign-spec.md`
- 范围：Web UI 全面重构；额外按用户指示将「持久请求日志」改为默认开启（见 §1.3）
- 工作区状态：全部改动停留在工作区，未提交、未推送

## 1. 实施结果

1. **统一设计语言**：新增 `tokens.css`（颜色/间距/字号/圆角/层级/动效）与 `app.css`（壳层与组件样式），深色为默认主题，浅色由 `[data-theme="light"]` 覆盖；`color-scheme` 从 meta 标签迁移到 `[data-theme]`，表单控件与滚动条随主题联动。
2. **OpenCode 风格壳层**：侧栏改为 5 个分组（仪表盘 / 资源管理 / 可观测 / 配置 / 高级），新增主题开关、跳转主内容链接、移动端抽屉导航与 13 条路由的键盘可达性；字体切换为 Geist Sans / Geist Mono，正文 13px（对齐 OpenCode 的 13px/440 密度）。
3. **通用组件库**：新增 Modal、ConfirmDialog、Drawer、Toast（Provider + useToast）、DataTable、Tabs、Pagination、Skeleton、FilterBar/SearchField、焦点陷阱与滚动锁；`window.alert/confirm/prompt` 全部移除（grep 为 0）。
4. **页面重构**：13 个页面（总览与实时监控合并为一个页面的双视图，`/monitor` 重定向）全部保留并按 §2 清单重构；列表使用服务端筛选/排序/分页参数；危险操作统一确认弹窗；订阅原文改为抽屉内显式查看；1440×900 下各页高度 ≤900px。
5. **样式清理**：按「源码实际使用集」两轮裁剪，`base.css` 3580→432 行、`mesh.css` 549→141 行；最终 CSS 产物 60.94 kB（设计升级中期为 105.66 kB，原始基线 82.49 kB）。
6. **日志默认开启（用户指示）**：`config.example.json` 中 `logging.requests.enabled` 改为 `true`；Go 缺省语义改为「字段缺省即开启」（`enabled=false` 可关闭）；文档同步。日志库启动时仅建空表，无任何预置/示例记录，界面只展示真实请求。
7. **浏览器实测**：CDP 走查 45 项 + Playwright 11 项（Chromium + Firefox）全部通过；覆盖深色 13 页、浅色 13 页逐页、总览双视图、图表 token 配色随主题切换、Key 创建/改名/删除与 Esc/确认弹窗；控制台仅登录前预期的 401。
8. **无 Key 匿名排名**：未配置 Zen/Go Key 时，排名引擎把匿名可用模型视为可路由（免费成本 0），不再把整个目录判为「未准入」；模型管理页补了显式刷新、排名生成时间与无可准入提示。

## 2. 页面改造清单

| 页面 | 文件 | 主要改动 | 保留的旧功能 |
|---|---|---|---|
| 总览（合并实时监控） | `OverviewPage.tsx` | token 化指标卡与趋势图；双视图（运行概览 / 实时监控）；日志表 DataTable；指标补单位/时间范围 | 原总览全部 + 3s 轮询、SSE 日志流、暂停、状态码/模型/端点分布；`/monitor` 重定向 |
| 额度看板 | `QuotasPage.tsx` | 进度条/标签 token 化；补充单位与刷新说明 | Go 账户额度窗口、模型清单、错误展示 |
| 模型管理 | `ModelsPage.tsx` | DataTable + 搜索/任务筛选；「优先级 / 综合排名」列；恢复与手动切换接入 Toast | 排名排序、可用性、Zen/Go/匿名、路由、恢复/转手动 |
| 代理节点池 | `NodesPage.tsx` | DataTable 服务端排序/分页；FilterBar + 搜索防抖；详情改 Drawer；GeoIP 4 条镜像常量逐字保留 | 全部筛选、分页跳转、启用/探测、GeoIP 上传/查询、详情字段 |
| 代理订阅 | `SubscriptionsPage.tsx` | 状态表改 DataTable；删除确认弹窗；原文改抽屉；Toast 反馈 | 预设添加、URL/粘贴表单、启用切换、刷新、清理冷却、原文查看 |
| 模型轮转 | `RotationPage.tsx` | 切换成功 Toast；列表视觉统一 | sota/sweet 顺序、当前切换、暂停/不可用状态、切换记录 |
| 上游管理 | `UpstreamsPage.tsx` | Key/旧代理表改 DataTable | 上游地址、运行策略、Zen/Go Key 池、legacy 代理 |
| 请求日志 | `RequestsPage.tsx` | Tabs 组件；筛选表单 token 化；请求/尝试/事件表改 DataTable；清空日志改确认弹窗；游标分页保留；「最新」零值修复 | 持久请求、实时尝试、出口事件、详情抽屉、游标分页、清空 |
| 路由诊断 | `DiagnosticsPage.tsx` | 出口候选表改 DataTable；指标补单位 | 模型路由、匿名资格、出口候选评分、Key 诊断 |
| API 密钥 | `APIKeysPage.tsx` | DataTable；改名改 Modal；删除改确认弹窗；Toast | 创建、自定义值、启停、改名、删除、用量 |
| 系统设置 | `ConfigPage.tsx` | reveal 改密码 Modal + 抽屉；脏检查徽标 + beforeunload；保存前远端基线比对；冲突确认；只读预览标注 | 全部配置字段、reload、reveal、账户更新、egress 开关 |
| 接口调试 | `PlaygroundPage.tsx` | 表单栅格与结果样式统一 | 协议/模型/Key 选择、真实推理请求与结果 |
| 接入手册 | `AccessPage.tsx` | 文案改为「系统设置」；代码卡样式适配深色 | 网关地址、鉴权说明、5 个 SDK 示例与复制 |
| 登录 | `LoginPage.tsx` | token 化卡片与图标 | 会话跳转、登录、错误提示 |

## 3. 通用组件与设计规范

- **Token**：`webui/src/styles/tokens.css`（颜色、间距 4px 基线、字号字重、圆角、阴影、z-index、动效、侧栏宽度）。
- **壳层与基础样式**：`webui/src/styles/app.css`（布局、导航、按钮、表单、徽标、提示、Modal/Drawer/Toast/DataTable/Tabs/分页/骨架/筛选、recharts 主题、响应式）。
- **组件**：`Modal`、`ConfirmDialog`、`Drawer`、`Toast/useToast`、`DataTable`、`Tabs`、`Pagination`、`Skeleton`、`FilterBar/SearchField`；焦点陷阱/滚动锁在 `lib/use-focus-trap.ts`。
- **无障碍**：对话框 `role="dialog"` + `aria-modal` + 焦点陷阱 + Esc；表格 `aria-sort`/caption；Tabs 方向键；Toast `role=status/alert`；全局可见焦点环与跳转主内容链接。
- **主题**：深色默认、浅色可切换，偏好存 `localStorage("mesh.theme")`；图表颜色使用 `var(--token)`。

## 4. 功能完整性验证

- 13 条路由 + 登录页全部可达；`/monitor` 重定向到合并后的总览；侧栏映射与 `webui-redesign-spec.md` §4.1 一致，未删除任何字段、按钮或 API。
- 所有按钮均连接真实接口：Key 创建/改名/删除、节点启停/探测、订阅 CRUD/刷新/清理、轮转切换、模型恢复/手动、配置保存/reload/reveal、账户更新、Playground 推理。
- `PUT /api/config` 负载仍由 `configUpdateFromView` 生成，未新增字段；保存前会重新 GET 基线比对，远端变化时要求确认。
- 持久日志默认开启后，实测两笔真实 403 请求进入列表（含模型、渠道、掩码 Key、耗时、结果），游标分页控件正常。
- 敏感信息：密钥默认掩码；订阅原文仅在显式抽屉中展示且不写日志/不截图；截图逐张复核无明文凭据。

## 5. 测试结果（实际执行）

| 检查 | 命令/方式 | 结果 |
|---|---|---|
| 前端类型与构建 | `cd webui && npm run build`（tsc --noEmit + vite） | 通过（每个任务后执行，最终产物 JS 815.66 kB / CSS 60.94 kB） |
| Go 构建 | `go build ./...` | 通过 |
| Go 静态检查 | `go vet ./...` | 通过 |
| Go 测试 | `go test ./...` | 全部通过（含更新后的 `internal/config` 日志默认值测试） |
| 遗产对话框 | `grep -rn "window\\.(alert|confirm|prompt)" webui/src` | 0 条 |
| CSS 裁剪 | `node project-context/artifacts/redesign-backup/prune-css.mjs --apply` | base 3580→432 行、mesh 549→141 行 |
| 浏览器实测 | Edge headless + CDP，1440×900 与 800×900 | 45 项检查，0 失败；1 条控制台 401（登录前会话探测，预期）；0 网络错误 |
| Playwright E2E | `cd webui && npx playwright test`（Chromium + Firefox） | 11 项通过（setup 1 + 每浏览器 5 项）：深色 13 页、浅色 13 页、图表换色、`/monitor` 重定向、Key 流程 |
| 截图证据 | `project-context/artifacts/redesign-test/screenshots/` | 深色页面、浅色总览、日志有数据、窄屏、总览双视图、模型分页、配置分区 |
| 页面高度 | Playwright 逐页测量 `scrollHeight`（1440×900） | 13 页全部 ≤900px（模型页 11 条/页；配置分区；总览双视图） |
| 回退快照 | T5 `SubscriptionsPage.tsx` 快照抽样比对（394 行 vs 当前 489 行） | 快照内容完整；未执行实际工作区回滚演练 |

浏览器实测覆盖：登录、13 页渲染与横向溢出、深/浅主题切换、总览双视图、Key 创建→改名 Modal→Esc 关闭→删除确认、节点搜索写入地址栏、配置分区页签、真实网关请求→日志列表与分页、窄屏导航、Tab 聚焦。

未执行：移动端真机、Safari/WebKit。Playwright 已配置进 CI（`webui-e2e` 任务），本轮未提交/推送，尚未在 GitHub 上实际触发。

## 6. 文件变更

**新增**
- `webui/src/styles/tokens.css`、`webui/src/styles/app.css`（含 `.chart-series-*` / `.chart-stop-*` 图表类规则）
- `webui/src/lib/theme.ts`、`webui/src/lib/use-focus-trap.ts`
- `webui/src/components/ui/{Modal,ConfirmDialog,Drawer,Toast,DataTable,Tabs,Pagination,Skeleton,FilterBar}.tsx`
- `webui/playwright.config.ts`、`webui/e2e/{serve.mjs,auth.setup.ts,webui.spec.ts}`
- `project-context/specs/webui-redesign-spec.md`（实施规格）、本报告
- `project-context/artifacts/redesign-backup/`（T1–T5 快照、裁剪脚本与报告）、`project-context/artifacts/redesign-test/`（验证脚本、报告与截图）

**修改**
- 全部 13 个页面（含合并后的总览）、`AppShell`、`LoginPage`、`main.tsx`、`index.html`
- `webui/src/styles/base.css`、`mesh.css`（裁剪与 token 化）
- `internal/webui/dist/*`（重新构建的内嵌产物）
- `config.example.json`、`internal/config/config.go`、`internal/config/logging_test.go`、`docs/configuration.md`（日志默认开启）
- `webui/package.json`、`webui/package-lock.json`（新增 `@playwright/test` devDependency）
- `.github/workflows/checks.yml`（新增 `webui-e2e` 任务）、`.gitignore`（Playwright 产物）
- `README.md`、`AGENTS.md`（测试命令与 CI 说明）
- `project-context/README.md`（索引登记）

**删除**
- 仅删除 `base.css`/`mesh.css` 中未被引用或已由 `app.css` 接管的旧规则，以及构建产生的旧哈希资源文件；无页面、组件、API 或配置项被删除。

## 7. 剩余问题与建议

1. **未保存提醒范围**：`beforeunload` 覆盖刷新/关闭；应用内路由切换提醒受 react-router v6 非 data router 限制未实现。
2. **既有测试缺口**：auth/config/api-keys/rotation/rankings/debug 端点仍无 Go 测试（重构前即如此），本次未扩大范围。
3. **Bundle 体积**：JS 约 816 kB（新增组件，无运行时依赖）；recharts 占比未变。
4. **订阅原文功能**：为保留原能力，仍提供显式查看入口，但改为抽屉内展示并提示敏感；若需彻底禁止查看，需产品决策。
5. **本地快照/验证产物**：`project-context/artifacts/redesign-backup/`（T1–T5 快照、裁剪脚本与报告）与 `artifacts/redesign-test/`（验证脚本、报告、截图）均为未跟踪文件，是否保留由用户决定；快照覆盖 T1–T5，未覆盖 T6–T10，且未做实际回滚演练；测试用 `config.json`、日志数据库与浏览器配置在交付时已清理。
6. **Playwright CI 尚未实际触发**：`webui-e2e` 任务已写入 `checks.yml`，但本轮未提交/推送；首次运行需要下载 Chromium 与 Firefox，CI 耗时会高于纯构建任务。
7. **浏览器覆盖**：Chromium 与 Firefox 已实测；Safari/WebKit 与移动端真机未覆盖。
8. **空数据图表**：后端返回零值分钟点时，面积图为零高度平线、柱状图不绘制矩形；当前按真实数据展示，若需显示「暂无数据」空态，可增加全零判断（未改动）。
9. **页面适配范围**：1440×900 下 13 页均无页面级滚动；更窄窗口或超出分页/分区范围的数据仍会滚动（模型 11 条/页、配置分区、总览双视图）。字体为 Geist Sans/Geist Mono + 中文回退，正文 13px；Cloudscape 的 Open Sans 属其自有视觉层，未采用。

## 8. 启动与预览

标准流程（本次以同一流程在本机验证，测试端口 18080/18081；8080/8081 被既有实例占用）：

```bash
cd webui && npm ci && npm run build
cd .. && go build -trimpath -ldflags "-s -w" -o mesh ./cmd/mesh
cp config.example.json config.json   # 修改 webui.password；如需仅本机访问，把 listen/webui.listen 改为 127.0.0.1
./mesh -config config.json
```

访问 `http://<webui.listen>/`（默认 `:8081`）→ 使用 `webui.username/password` 登录。开发联调：`cd webui && npm run dev`（Vite 5173，`/api` 代理到 8081）。

WebUI E2E（Chromium + Firefox）：

```bash
go build -o mesh ./cmd/mesh          # Windows: -o mesh.exe
cd webui && npx playwright install chromium firefox   # 首次
npx playwright test
```

Playwright 会自动创建临时配置、启动 mesh（18080/18081）、登录并运行 11 项用例，结束后清理临时数据库。若本机已有实例占用 18081，可设置 `E2E_BASE_URL=http://127.0.0.1:18081 E2E_PASSWORD=<密码>` 复用。

日志默认开启后，首次启动会在配置指定的 `logging.requests.state_path` 创建空库；只有真实请求会写入，不包含任何预置记录。
