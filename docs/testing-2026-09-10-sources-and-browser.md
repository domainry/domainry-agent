# 来源权限与网页日常工作验收

本次验收使用当前 Agent、Agent SDK、Identity 和 Connector 代码，以及临时 SQLite。
浏览器使用 Codex 内置浏览器，测试服务仅监听 `127.0.0.1:8092`。
模型和知识服务均为本地 HTTP 协议夹具；下列通过项不代表真实 Verdent 多步理解或实际知识库命中已经验收。

## 来源权限变更

- 冻结输入与摘要保存服务端生成的来源运行引用；普通后续回复、跨会话历史搜索和原文读取保留来源依赖。引用沿用现有 JSON 存储，没有新增数据迁移。
- 读取消息、运行或 SSE，以及继续使用相关内容执行前，按当前身份重新检查来源。访问失败时返回明确状态，隐藏对应的回复、步骤和交互内容；不会改写原始账本。
- 摘要的来源不可读取时，从原始消息重建，保留仍可读取的用户约束。权限恢复后，可重新从原始记录恢复被省略的资料。
- 旧历史工具结果没有 Run ID 时，使用原始消息 ID 找回所属运行，不能把缺少来源字段当成公开内容。
- 前端拒绝在受限快照上合并延迟到达的工具事件，隐藏旧草稿、结果和交互卡片。修复了已完成运行的草稿清空后，详情窗口不显示最终回复的问题；详情改为读取已保存的最终步骤。

代码证据：

- [来源检查](../internal/application/conversation_sources.go)、[上下文与摘要](../internal/application/conversation_context.go)、[来源快照存储](../internal/infrastructure/persistence/database/agent/conversation_sources_store.go)。
- [集成测试](../integration/conversation_sources_integration_test.go)：当前与旧格式记录、派生回复、跨会话搜索及读取、撤权后的消息 / Run / SSE、重启、权限恢复、摘要重建与用户约束保留。
- [真实 Identity / HTTP 权限测试](../internal/assembly/web/conversation_knowledge_test.go)、[前端状态测试](../frontend/src/conversation-state.test.ts)。

知识 Provider 目前仍通过当前权限重新查询并比较完整规范化 JSON。合法内容、排序或元数据变化也可能使原结果无法复用；这不是逐文档 ACL / 版本验证的最终实现。来源检查也不意味着撤回用户已读取的副本，或已经完成记忆、待办、成果文件中派生资料的保留和清理策略。

## 实际浏览器操作

| 场景 | 页面观察结果 |
| --- | --- |
| 新建会话，通过消息创建三条待办 | 显示创建工具完成；三项分别为整理访谈记录、核对费用、提交周报 |
| 发送“把第二项改到周五” | 查询待办及时间后，确认卡片准确显示原批次第 2 项“核对费用”，新截止日期为 `2026-09-11 · Asia/Shanghai` |
| 在确认前刷新，再批准 | 目标、原日期和新日期保持一致，批准后列表中的第二项更新 |
| 待办窗口只看当前会话，完成第一项并刷新 | 仅显示该会话的三项；第一项仍为完成状态，第二项日期保留，其余原始项次不变 |
| 查看第一次创建待办的旧运行 | 显示当时保存的三项结果和最终回复，没有把后来修改的状态冒充原始结果 |
| 对话请求记住周报格式，确认前刷新 | 恢复相同的记忆标题与内容；批准后个人记忆列表出现一条记录 |
| 勾选本次个人记忆操作范围，再请求修改 | 已有记录更新为“每个项目先写结论，再列进展和风险”，没有重复新增 |
| 在个人记忆窗口停用并刷新 | 修改后的内容保留，启用开关仍为关闭 |
| 有文档权限时查看知识读取结果 | 运行详情可见测试文档正文及最终回复 |
| 撤销真实 Identity 文档组权限，再刷新运行详情和整页 | 详情不再提供步骤、正文或回复；历史回复显示来源不可读取，原始用户提问保留 |
| 恢复文档组权限，再刷新 | 原始正文、工具结果和回复重新可读；工具显示为“搜索知识库”和“读取文档” |

待办和个人记忆窗口均做了实际截图检查。上述操作分别使用三个临时测试服务；它们的测试进程均已正常结束，8092 监听已关闭。没有启动或修改 8091 上的部署。

## 已通过的验证

```sh
go test ./...
(cd ../domainry-agent-sdk && go test ./...)
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./integration ./internal/assembly/web
go vet ./...
go build ./...
npm --prefix frontend test
npm --prefix frontend run build
```

Agent 与 SDK 的 Go 格式检查通过，前端状态测试共 16 项通过。浏览器测试对应的进程均完成后续后端断言并正常退出：

```sh
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestPersonalTodosThroughIdentityHTTP$' -count=1 -v
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestPersonalMemoryThroughIdentityHTTP$' -count=1 -v
AGENT_TOOL_UI_ACCEPTANCE=1 go test -race ./internal/assembly/web -run '^TestKnowledgeToolsUseLiveIdentityDocumentPermissionsAndStoredResultChecks$' -count=1 -v
```

每次只能运行一个浏览器验收服务。此模式等待实际网页操作；完成后调用测试控制端点 `POST /__acceptance/finish` 继续余下断言。
知识测试额外提供 `POST /__acceptance/knowledge/revoke` 和 `POST /__acceptance/knowledge/restore`，仅用于改变该临时环境的测试权限；生产路由不挂载这些控制入口。

## 剩余验收范围

- 真实外部模型完成“找回讨论 → 创建待办 → 修改第二项 → 保存偏好 → 重启后继续”的串联流程。当前网页操作分别由确定性模型夹具驱动。
- 真实 Verdent 文档命中、逐文档权限 / 版本契约、可核对的来源引用展示。
- 网页删除与拒绝的补充场景、窄屏布局、活动运行与旧运行窗口并行查看。
- TODO 中尚未完成的附件、成果、业务能力、外部账号、后台任务、提醒、依赖发布与部署验收。
