# C04 增量：停止后的原操作回执核查

日期：2026-09-13。此记录覆盖转交前的一项必要基础；本批验收时转交、完成条件核对与结构化分歧仍未完成，完整清单为 3／25。随后完成的转交实现另见 [C04 转交验收](testing-2026-09-13-c04-transfer.md)，完成条件及分歧仍待实现。

旧 `Resume` 会重新进入执行循环，而部分工具的 `Reconcile` 可以重新提交幂等操作。因此增加独立的 `inspect_outcome` 决定，指定原 `run_id / step / call_id`，只查询已记录写操作的原回执。原运行必须已取消、失败或等待核查。核查不创建执行租约、不启动模型，也不恢复任务；原结果明确后，按新要求继续仍需要独立操作。

核查使用原调用、冻结工具定义、原幂等键和已保存的准确确认记录。持久化的临时核查令牌只允许观察这项回执，不能作为执行授权；令牌不进入 JSON／Business RPC。并发查询有占用与过期检查，完成查询后令牌不再通过确认验证。原 worker 的迟到确定回执优先，之后的“不确定”查询不能覆盖它。缺失回执、查询失败、输出格式错误均保留不确定状态，不推断操作未发生。

Tools SDK 增加可选 `OutcomeInspector`，Tools 注册表必须显式登记 `InspectOutcome`。注册表拒绝携带核查令牌的 Invoke 和普通 Reconcile。账号写入工具只调用 Integration 的 `ReadConnectionAccountWriteReceipt`，结构化记录工具只调用原 owner 的 `Receipt`。计划工具的 Reconcile 可能重新创建计划，因此没有登记只读核查。其余没有提供该端口的宿主明确返回不可用，不能退回普通恢复入口。

应用继续检查当前执行权限、冻结 Agent 工具范围、原始资料来源及工具结果权限。工具组合包装保留可选端口；不支持的组合不会悄悄调用写入。页面逐项显示停止后的未知写操作，并保留核查人、时间和原结果引用。核查在运行审计中单独展示，不增加实际工具调用次数，也不把查询事件写成原操作的执行耗时。

| 验证 | 实际结果 |
| --- | --- |
| 持久化 | 旧约定执行不能恢复；核查不入执行队列；准确原确认可核对，修改参数及跨用户被拒绝；未知回执继续阻止新执行；原迟到回执不被覆盖；明确结果后可显式启动新约定。 |
| HTTP／Identity／SQLite／外部协议夹具 | 外部服务实际写入 2 个文件，每个幂等键只收到 1 次 POST。第二项落盘后断开响应，重启 Agent 后用原键查询；第一次 GET 隐藏回执，第二次 GET 返回原记录。权限撤销期间无 GET；重复核查已知结果不新增 GET；模型始终只调用 1 次。 |
| Chrome | 从委派页选择唯一未知写操作，点击核查，打开原执行回执并查看核查记录。无额外操作确认、无自动继续；刷新后保留结果和独立继续入口。桌面、390px 页面无横向溢出，JS 错误为零。 |
| Go 与 SDK | 受影响应用、持久化、HTTP Module、Module、Remote、Server、Capability 包通过；Agent SDK、Tools SDK、Tools 全套通过。Business RPC 原契约摘要保持不变。 |
| 并发检查 | 核查场景的应用、持久化及真实 HTTP race 检查通过。 |
| 前端 | 65 项状态测试通过，生产构建通过；保留既有 Vite 大 chunk 提示。 |

关键代码：[应用入口](../internal/application/conversation_outcome_inspection.go)、[持久化及令牌验证](../internal/infrastructure/persistence/database/agent/conversation_outcome_inspection_store.go)、[组合边界测试](../internal/application/conversation_outcome_inspection_test.go)、[外部 HTTP 验证](../internal/assembly/web/conversation_peer_outcome_test.go)、[页面验收脚本](../frontend/tests/peer-outcome.browser.mjs)。本次日志和截图归档在 [验收证据目录](evidence/2026-09-13-c04-outcome-inspection)。协议服务与模型均为隔离夹具，不能据此宣称真实第三方服务或自然模型成功率已验收。

转交前发现的具体差额：当时 `conversationDelegationRemainingBudget` 只统计当前 `ConversationID`，转交需要累计历史接单会话；委派身份、任务／会话身份与具体执行身份需要分开。后续转交实现已按这些要求补齐。旧执行仍须先停止并核实不明效果，新接收方只能在当前权限下读取保留的证据。
