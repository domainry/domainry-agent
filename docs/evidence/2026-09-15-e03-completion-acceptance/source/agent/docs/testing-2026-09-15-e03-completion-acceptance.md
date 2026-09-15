# E03 完成验收与恢复决策验收

日期：2026-09-15。本轮为普通后台任务补齐独立完成验收。任务运行结束、Agent 提交交付和完成条件通过现在是三个可区分的持久状态；模型 `stop` 不再足以完成带明确约定的新任务。E03 验收完成后，完整清单进度为 **9／25**；V01 本轮增加同模型／任务／预算对照，完整七类基线仍随后续条目继续建设。

## 完成契约与程序核对

- [Agent SDK 完成契约](../../domainry-agent-sdk/conversation_task_completion.go)定义执行 Agent 的结构化提交、逐项判断、程序核对结果、用户／Agent 复核、准确工具回执、Knowledge 成果版本和不可变修订历史。带显式 `brief` 的普通任务使用 `assessed` 模式；没有显式约定的旧调用保留 `legacy_response` 兼容模式。
- `completion_submit` 只进入当前 `assessed` 普通后台任务的模型工具目录。写入必须绑定当前任务、执行 Run、约定修订和完成记录 revision，并覆盖当前约定的每项完成条件。任务结束时没有提交会保存 `execution_end` 记录并进入 `awaiting_review`，不会生成完成事件。
- `verification_rules` 可对提交 JSON 数据或原工具回执做程序核对。回执规则检查准确工具、参数 Schema、结果 Schema、完成语义、最少独立效果数和原回执 SHA-256；同一幂等效果的复制不能重复计数，`accepted`、`uncertain` 与 `completed` 保持区分。数据 Schema 只证明结构符合规则，页面和工具说明明确保留实际业务事实仍需证据判断的边界。
- 无程序规则的条件保存执行 Agent 的模型判断、依据和引用；用户或另一 Agent 可用当前 delivery digest 与 revision 逐项复核。程序核对项始终由服务端重新计算，Agent 或用户声称满足不能覆盖失败结果。

## 恢复、版本与来源

- 未满足、未知、缺失提交和旧约定交付都保持 `awaiting_review`。任务可以从新的不可变 Run 继续，先前 Run 与完成修订保留；下一次提交使用当前完成 revision，乐观并发和 `client_id` 幂等在 SQLite 事务内处理。
- 约定变化会清除旧完成事件和结果，把旧完成记录保留为历史且标记为过期。页面修改完成条件时，只保留条件文本仍位于相同索引的程序规则，避免隐藏地把旧规则绑定到新要求。
- 当前完成详情、完成历史、`completion_submit` 和 `task_review` 的保存回执在再次读取时，重新核对原运行、准确工具回执、成果版本／SHA 和当前权限。来源失效时隐藏完成依据并显示来源不可用；历史引用不能借保存内容绕过当前读取权。
- 会话导出、删除和主体生命周期包含完成记录表；迁移 v33 创建不可变 `(owner, task, revision)` 账本。SDK、HTTP、RPC、Module 与 SaaS 入口均使用同一服务语义。

## 页面与状态区分

- [任务完成页面](../frontend/src/TaskCompletion.tsx)显示当前提交摘要、逐项方法／结果／依据、阻塞原因、原回执、提交／复核运行和准确成果版本。`awaiting_review` 有独立筛选与状态，不与运行中或已完成混用。
- 用户复核表单对程序项只读，对其余条件逐项记录 `met`、`unmet` 或 `unknown` 及依据。提交绑定当前 digest/revision；旧页面或并发复核返回冲突，重复同一 `client_id` 安全重放。
- 完成历史按 revision 分页并保留最初未确定的 Agent 评估和后续用户决定。真实 Chrome 场景覆盖等待验收、用户复核、两版历史、刷新后的服务端状态和 390 px 布局；登录后的 JavaScript 错误、控制台错误与警告均为 0。

## 同任务、模型与预算对照

受控夹具使用相同目标、输入、`planning-fixture` 模型和 `{max_steps:1, max_tool_calls:1, max_output_bytes:2048, timeout_seconds:30}` 预算，各执行 1 次模型调用、1 个步骤、0 次工具调用，并返回相同的“模型声称任务已经完成”文本。

| 处理方式 | 服务端状态 | 独立验收 | 人工介入 | 观察结果 |
| --- | --- | --- | --- | --- |
| 旧兼容基线 `legacy_response` | `completed` | 只检查回复已持久保存 | 0 | 模型 stop 会形成兼容完成记录 |
| 新约定 `assessed` | `awaiting_review` | 保存 `execution_end`，条件未知 | 1 | 相同 stop 不再被误报为验收通过 |

这项对照证明本轮改变的是完成判定，不是模型能力或工具数量。它把一次潜在假阳性转成可见待处理；完整 V01 仍需随剩余能力累计完成率、遗漏、人工介入、耗时和用量基线。

## 验收

- Agent SDK 全量测试通过；Agent 执行、应用、持久化、HTTP、Module、RPC、Server、Integration、命令与 Definition 包通过，完整 `internal/assembly/web` 包通过。
- 完成规则、应用工具可见性与 SQLite 完成／恢复边界的针对性 `-race` 通过。程序检查失败不能被 Agent 或用户覆盖；停止但未提交、恢复后新 revision、幂等复核、旧 revision 冲突和跨 owner 隔离均有持久化测试。
- 真实 Identity／HTTP／SQLite 链路验证 Agent 直接通过、Agent 未确定后用户通过、两版历史、重复复核和旧 revision 冲突；受控对照记录相同模型、预算、步骤和工具调用数。
- 前端 87 项测试、TypeScript 检查和 Vite 生产构建通过；真实 Chrome 页面三项验收通过。
- Agent 与 SDK `git diff --check` 通过，相关目录没有遗留调试错误码或调试输出。

命令、结构化报告、源码摘要、日志与截图见[验收证据](evidence/2026-09-15-e03-completion-acceptance/commands.md)。
