# C07 异步协作恢复与全目标预算验收

日期：2026-09-14。本轮补齐 C07 剩余的全目标预算、通信速率、重复问题答复关联和 Agent 配置变化后的恢复入口。C07 验收完成，完整清单进度为 **6／25**。

## 共享预算与通信

- [工作预算契约](../../domainry-agent-sdk/conversation_task.go)定义输入／输出 token、累计模型时间及可选模型费用；根委派创建唯一账本，后续委派和转交只能继承相同预算及剩余额度。[预算存储](../internal/infrastructure/persistence/database/agent/conversation_work_budget_store.go)在工作区与工作根范围内原子累计全局委派数，并为并行模型请求预留额度，避免多个 Agent 同时透支。
- 模型调用前以实际序列化请求字节数作为保守输入 token 上界，并预留最大输出 token、超时时间与当时冻结的模型价格；调用完成后按提供者返回的实际 token、实际耗时和冻结价格结算。提供者没有返回旧调用用量时保守记账，且不会把未知费用显示成已知零值。
- [消息存储](../internal/infrastructure/persistence/database/agent/conversation_peer_messages_store.go)保留每项委派 128 条总上限，并增加每个已认证发送主体在同一委派内每分钟 16 条的速率限制。相同 client ID 的幂等重放先返回原回执，不重复占用速率额度。

## 问题答复与恢复

- [答复投递](../internal/infrastructure/persistence/database/agent/conversation_peer_delivery_store.go)只合并文本完全规范化相同、发送主体及角色、参与人版本、方向、目标会话、资料摘要、来源摘要和当前任务约定均相同的未关闭问题。一条答复保存全部原问题 ID，并逐项关闭原等待事项；其他问题保持打开。操作确认使用独立交互记录，不进入问题合并，也不扩大原授权范围。
- Agent 停用、配置或模型变化导致旧接单失效后，[转交应用层](../internal/application/conversation_transfer.go)和[持久化层](../internal/infrastructure/persistence/database/agent/conversation_transfer_store.go)允许选择同一个逻辑 Agent 的较新配置。系统创建新的会话、任务和不可变接单记录，保留当前任务约定、来源、依赖、已发生效果、逐委派剩余额度以及整个工作根的共享预算。相同快照会拒绝无意义重启。
- [协作详情页](../frontend/src/CollaborationDialog.tsx)显示整个工作预算及实际用量；识别 `agent_changed`、`agent_disabled` 和 `model_changed` 后提供“按当前配置重启”。停用状态下提交按钮保持不可用，配置修复后可在原详情继续工作。

## 验收结果

- SDK `go test ./...` 通过。
- 工作预算、通信限流、等价问题答复和同 Agent 恢复的针对性 race 测试通过；覆盖并发预留超额拒绝、跨用户全局委派数、实际用量与价格结算、免费幂等重放、非等价问题保留、相同快照拒绝及较新快照恢复。
- 前端 84 项测试和生产构建通过。
- 产品 Go 代码包全部通过。仓库根直接运行 `go test ./...` 会把 `docs/evidence` 中为审计保存的局部 Go 源码快照当成可编译包，因此使用 `go list` 排除证据归档后运行全部实际代码包；没有修改既有封存证据来伪造根命令通过。
- `TestPeerSameAgentRecoveryHTTPAndBrowser` 的真实 Identity／HTTP／SQLite／Chrome 流程通过：旧任务先记录 `agent_changed`，通过实际 Agent 更新接口发布新配置，页面提交同 Agent 恢复，服务端创建新会话／任务／接单记录并复用同一工作预算。桌面截图覆盖预算与恢复表单，390 px 截图验证恢复后的详情无横向溢出。

命令、结构化报告、源码摘要和截图见[验收证据](evidence/2026-09-14-c07-collaboration-recovery/commands.md)。
