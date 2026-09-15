# K01 动态 Skill 与可复用流程验收

日期：2026-09-15。本轮在现有 Agent Profile、冻结执行上下文、后台任务、成果版本和协作权限上补齐动态 Skill 目录、按需加载、任务反馈、能力改进候选、V01 发布门槛和配置回退。K01 验收完成后，完整清单进度为 **16／25**；本轮只登记 K01 的增量对照案例，完整七类 V01 基线继续累积。

## 契约与 owner 边界

- [SDK Skill 契约](../../domainry-agent-sdk/conversation_skill.go)提供摘要、精确版本、资源读取、任务反馈、候选、评估、发布和回退类型；HTTP／RPC／Remote／Module 使用同一声明。旧宿主不实现可选接口时保持原有能力。
- Agent 的 `ConversationToolDefinition` 继续复用 `domainry-tools-sdk.Definition`。邮件、日历、搜索等可复用业务工具仍由 `domainry-tools` 注册和执行；`skill_load` 读取冻结在当前 Agent run 内的配置，不能独立于 Agent 账本，因此由 Agent 的 ToolHost 包装层提供，再把其他调用交给基础 ToolHost。
- Skill 声明只能引用 Agent 已有工具。Profile 编译、任务工具冻结和每次调用授权仍会分别核对，Skill 不能增加工具权限或绕过连接可用性、确认和回执。

## 摘要目录、按需读取与版本冻结

- 目录只返回名称、描述、版本、输入输出约定摘要、流程步数、资源元数据和允许工具，不返回 Instructions 或资源正文。正文／Workflow 与单项资源使用不同精确版本路径读取；资源详情读取不会修改部署中的静态配置。
- Agent 系统提示只装配 Skill 摘要。模型需要正文或资源时调用 `skill_load`，正文读取不携带资源内容，资源读取也不重复携带完整正文。
- 前台 run、普通后台任务、计划后台任务和委派执行都冻结 Agent revision、提示词版本及完整 Skill 版本。旧 run 只能读取自己的冻结版本；新发布只影响后续 run，配置变化不能把旧版本静默替换成新版本。
- 本轮修复了计划任务未保存 Agent 快照的问题：计划入口现在使用与前台发送相同的 Agent 选择规则，在构建工具范围前选择冻结快照，并把它持久化到 task，任务反馈可准确引用实际 Agent 与 Skill 版本。

## 反馈、候选、评估、发布与回退

- 已完成任务可记录 `adopted`、`revised` 或 `failed`，并绑定实际 task、run、可选成果版本、Agent revision、提示词版本、目标 Skill 和所有冻结 Skill 版本。未完成任务、无对应执行、错误成果版本或不属于任务的 Skill 会被拒绝。
- Skill、Agent 提示词和委派策略候选必须关联真实反馈，并匹配当前准确基线。候选创建时保存基线版本及完整基线配置，后续并发配置变化会阻止误发布。
- 只有登记了相同套件／场景、候选完成数不低于基线、遗漏不增加、无回归且明确通过的 V01 记录才能发布。发布和回退都使用 revision 与幂等键。
- 第一次把静态 Skill 发布成托管版本前，服务会把静态版本和完整正文／资源归档为不可变回退基线。真实流程验证 `v1 → v2 → v1`，存储测试同时覆盖后续 `v2 → v3 → v2`。回退只切换后续任务使用的配置，不删除历史运行、回执、成果或已经发生的业务效果。

## 页面与真实流程

- Agent 协作页增加“Skill 与改进”：先显示摘要，按需展开正文、输入输出约定、Workflow 和指定资源；配置人员可以从完整资源内容创建候选，登记 V01、发布和回退。
- 后台任务详情增加任务结果反馈，展示准确 Agent、提示词和 Skill 版本。候选卡展示反馈、基线、评估、状态和不可变首次部署基线。
- [真实 Chrome 脚本](../frontend/tests/dynamic-skill.browser.mjs)操作编译后的页面、真实 Identity HTTP 和 SQLite，完成任务反馈、摘要／正文／资源分段读取、保留资源的候选创建、V01 登记、v2 发布、v1 基线归档和回退。
- 桌面和 390 px 布局无横向溢出；脚本记录 0 个 JavaScript 错误、console error、warning 和失败资源。五次写入分别为反馈、候选、评估、发布和回退，全部返回 200，配置管理过程没有额外调用模型。

SDK、Application、SQLite、HTTP、Remote、Integration、91 项前端状态测试、生产构建和真实浏览器结果见[验收命令与日志](evidence/2026-09-15-k01-dynamic-skills/commands.md)。
