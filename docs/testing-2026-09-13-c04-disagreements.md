# C04 结构化分歧处理验收记录

日期：2026-09-13。范围：当前部署、同一用户授权范围内的平权 Agent 委派。本文补充 C04 的分歧记录、证据对照、处理责任与页面；完整 C04 回归通过，C04 标记完成，整张清单为 4／25；下一项为 C05。

## 实际行为

- 委派双方 Agent 或用户可以记录不同结论，每条结论保存数据范围、时间范围、来源及版本、计算或推导过程，以及可选的不可变原回执。记录者、Agent 身份与来源运行边界由服务端确定；记录另一种解释不等于证明另一位 Agent 说过这句话。
- `delegation_update` 的 `action=disagreement` 支持 `raise / add_claim / decide`。发起方 Agent 或用户可决定补查、修订、请用户判断或采用某条实际存在的结论；决定必须包含四个维度的证据对照、依据和当前需求／约定／交付摘要。
- 补查和修订明确指定参与方 Agent 与后续工作；等待用户判断记录当前用户与具体问题。采用结论不会自动验收交付。新增证据重新打开分歧，已验收委派重新进入待验收状态。要求或交付变化后，旧处理决定必须重新核对。
- 未解决或旧版本分歧在存储事务内阻止验收；不能与验收并发绕过。每次变更按委派和分歧两个修订号核对，重复请求不产生重复结论、历史或通知。
- 分歧每次变更保存不可变快照。`delegation_disagreement` 工具与独立 HTTP 查询按每页两条读取历史。原决定者单独保存，转交后的当前负责方不会覆盖原决定者或原决定中的负责方。
- 后续负责方通过已有持久收件箱接收通知；旧分歧修订的待消费通知被替代，其他问题和操作确认不受影响。转交时，未解决分歧的责任随当前接收方转移，新任务仍需按新约定核对。
- 接收方每个新模型步骤读取当前分歧摘要，替换旧的当前摘要。摘要及其来源有界保存；超过 12 KiB 摘要或 64 个来源时保留待按需读取的分歧 ID，不把缺少上下文当作已经解决。
- migration 24 新增分歧历史及执行步骤来源表。模型输入冻结时，在同一事务保存其动态证据来源；恢复、下一步执行、工具调用和历史读取继续核对当前来源权限。私有附件不能隐式跨会话共享，权限撤回不能通过旧分歧或旧执行绕过。
- 页面可创建分歧、关联完成条件与原回执、补充结论、对照证据、指定后续负责方、查看历史和决定来源。来源不再可见时清理已加载记录，迟到的旧请求不能重新显示已不可见的分歧。
- 分歧解决后，仅移除当前分歧造成的验收阻塞；不改写原核对历史，不让其他尚未核实的完成条件自动通过。

证据对照中的文字是 Agent 或用户的评估；程序验证必填维度、版本绑定、来源和原回执，不保证自然语言判断一定正确。C05 的成果权限分离、E03 的通用遗漏续办与自然模型效果评估、C07／E05 的全目标预算仍分别保留待办。

## 代码入口

| 层次 | 当前实现 |
| --- | --- |
| SDK | [分歧契约](../../domainry-agent-sdk/conversation_disagreement.go)、[工具声明](../../domainry-agent-sdk/conversation_collaboration_tools.go)、[执行来源](../../domainry-agent-sdk/persistence/conversation_sources.go) |
| 领域 | [分歧变更校验和阻塞判断](../internal/execution/disagreement.go) |
| 应用 | [分歧、权限与模型上下文](../internal/application/conversation_disagreement.go)、[执行来源复核](../internal/application/conversation_sources.go) |
| 持久化 | [不可变历史与通知](../internal/infrastructure/persistence/database/agent/conversation_disagreement_store.go)、[步骤来源](../internal/infrastructure/persistence/database/agent/conversation_execution_store.go)、[迁移](../internal/infrastructure/persistence/database/agent/conversation_disagreement_schema.go) |
| 页面 | [分歧记录、决定与历史](../frontend/src/Disagreements.tsx)、[验收和待处理状态](../frontend/src/collaboration-state.ts) |
| 场景 | [真实工具链与 HTTP](../internal/assembly/web/conversation_peer_disagreement_test.go)、[Chrome](../frontend/tests/collaboration.browser.mjs) |

## 验证范围

模型、Identity、SQLite、外部回执和浏览器均使用隔离夹具，没有连接生产业务系统。

- 领域／存储：缺少任何证据对照维度、无后续工作、无关负责方、缺少采用结论与旧交付摘要均不能通过；覆盖新证据重新打开验收、当前约定变化、不可变历史分页和重复请求幂等。
- 责任与通知：无关 Agent 不能冒充发起方决策；旧通知不能继续唤醒；转交后责任归新接收方，原决定及历史保持不变。
- 来源：私有附件分歧不进入另一会话；模型步骤之前的来源边界不被后续资料污染，实际消费后仍检查权限。关闭并重新打开 SQLite 后，两份来源记录与冻结模型输入一致，重放不能移除来源，也不能引用未来步骤。
- Agent HTTP：发起方实际调用 `delegation_get → delegation_update(raise) → delegation_disagreement → delegation_update(decide)`；结果被压缩时，通过真实 `reference` 调用 `tool_result_read` 并拼接完整分页。保存的记录者和决定者对应真实工具步骤；用户随后明确验收。重开宿主后，完整分歧历史一致；撤回原时钟工具权限后，分歧详情隐藏，历史查询返回 403。
- Chrome：用户记录不同结论后无法验收；可请用户判断、采用有原回执支持的结论，再明确验收；刷新后能查看早期历史。桌面与 390px 窄屏没有横向溢出或 JavaScript 错误。

核心应用、领域、持久化、Module／Remote 与 SDK 全套测试通过；69 项前端状态测试通过。最终 HTTP／Chrome 与原回执、两种转交共四个场景通过（约 42 秒）；应用、持久化及完整协作 HTTP 的 race 检测通过（HTTP 约 177 秒）。日志、截图、命令与本批源码摘要保存在[证据目录](evidence/2026-09-13-c04-disagreements)。构建保留原有 Vite 大 chunk 提示。

## C04 与前批的对应关系

| C04 范围 | 证据 |
| --- | --- |
| 独立接单、运行、通信、交付与生命周期控制 | [协作基础链路](testing-2026-09-13-peer-collaboration.md)、[C03 要求变更与停止旧执行](testing-2026-09-13-c03-peer-communication.md) |
| 输入／输出契约与版本 | [结构化输入](testing-2026-09-13-c04-structured-input.md)、[逐项交付核对](testing-2026-09-13-c04-verification.md) |
| 转交、未明效果核实、累计执行额度与避免重复效果 | [原回执核查](testing-2026-09-13-c04-outcome-inspection.md)、[平权 Agent 转交](testing-2026-09-13-c04-transfer.md) |
| 交付负责方、验收方与委派目的 | 委派双方、原因和冻结能力记录；[C01 能力／权限／负载／成本依据](testing-2026-09-13-c01-agent-discovery.md)；工具声明要求说明委派收益，简单工作可以直接执行 |
| 分歧核对、采用依据、后续责任与页面 | 本文及本批场景测试 |

委派选择仍是基于当前目录依据的 Agent／用户判断，尚无全目标协调成本优化或自然模型完成率对照。未提供只读核查端口的工具继续保持未知效果阻塞；当前接收方还有未关闭的对外委派时，转交明确受阻，后续迁移策略归 C07。
