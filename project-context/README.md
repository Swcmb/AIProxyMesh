# project-context

本目录保存本项目的规格与设计记录。代码内注释与 `docs/` 是使用入口，这里是依据与决策记录。

| 文件 | 内容 |
| :--- | :--- |
| `specs/model-ranking.md` | 模型综合排名的完整定义：公式、四个维度、任务预设与准入条件 |
| `specs/egress-health-scheduling.md` | 出口节点健康筛选与预算调度规格：健康键、Wilson 准入、状态机、金丝雀调度、稳定池索引与分阶段验收 |
| `specs/webui-redesign.md` | Web UI 全面升级提示词：设计参考、各页面要求、实施阶段与验收标准 |
| `specs/webui-redesign-spec.md` | Web UI 改造实施规格（任务分解、验收门槛、回滚与验证通路） |
| `specs/webui-redesign-report.md` | Web UI 改造交付报告：页面清单、测试结果、文件变更与剩余问题 |
| `artifacts/redesign-backup/` | Web UI 改造 T1–T10 任务前文件快照，用于整文件回退；验收后处置由用户裁决 |
| `refactor-report.md` | 自包含重构与排名驱动轮转的变更记录、验证结果与破坏性影响 |
