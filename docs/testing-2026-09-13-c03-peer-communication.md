# C03：双向通信、需求依赖和版本采用

日期：2026-09-13。沿用当前 checkout，涉及 Agent、Agent SDK 和前端。C03 按核心 TODO 的完整条目验收；问题合并仍属于 C07，完整执行计划属于 E02。

## 通信与身份

`agent_message` 和 HTTP 消息入口支持 `message`、`question`、`reply`，回复绑定同一委派、当前需求／约定下的一条尚未回答的问题。服务端确定实际发件用户或 Agent、接收会话、来源执行；调用方不能提交这些身份。回复和原问题的 `answered_by_id` 在同一事务保存，重复 client ID 返回原回执，另一条回复不能重复关闭问题。

`next_step` 在下一次冻结模型输入时消费；`next_run` 由服务端绑定提交时的活动运行，等它结束后才进入下一轮。延后消息不会挡住后面的立即消息。消费与模型步骤冻结原子提交，保存运行、步骤和时间；运行中断后可以读取同一份输入。旧要求下的排队消息保留并标为已替代，不再进入新执行。委派详情展示最近 128 条消息，截取时明确显示不完整。

Agent 输入保留真实发件身份并声明不产生用户授权。系统要求变更通知使用独立类型；空闲会话被唤醒时保存 `peer_event` 元数据，主会话显示“Agent 协作通知”，不显示为用户发言。实际工具仍通过既有授权、确认、租约和提交检查。

依据：[SDK 通信契约](../../domainry-agent-sdk/conversation_collaboration.go)、[投递与回复](../internal/infrastructure/persistence/database/agent/conversation_peer_delivery_store.go)、[持久收件箱](../internal/infrastructure/persistence/database/agent/conversation_peer_inbox_store.go)、[模型输入](../internal/application/conversation_peer_inbox.go)。

## 定向传播与采用

委派可声明其他委派的要求依赖，限定同一用户和工作目标。依赖保存上游需求版本、约定版本、所选字段、实际值、摘要及来源。可选择目标、交付物、使用者、约束、完成条件、假设和截止时间，空字段集合表示全部。由已有委派发出新工作时，默认继承发起任务的要求依赖，可显式缩小字段范围。Agent 本身没有父子身份。

依赖最多 16 条，目标下最多 32 项委派，加入依赖时检查循环。变更按选定字段判断直接影响，并沿依赖图传播间接影响；同一事务停止受影响的旧运行、更新任务状态并给双方写入变更通知。无关字段变化不停止任务。已发生的工具效果和历史交付保留；旧 worker 无法继续冻结步骤或提交结果。

需求正文有 `brief.version`，正文或依赖变化另推进 `agreement_revision`。恢复时必须明确提供已核对的上游两个版本，不能只凭正文未变而暗中升级依赖。上游自身尚有未核对的间接变化时，下游不能恢复。新的执行输入包含直接和传递依赖的实际要求值，受既有输入大小限制和来源检查约束。

恢复请求与执行采用分别记录：接收方首次冻结模型步骤时才设置 `adopted_agreement_revision` 和时间。交付同时绑定需求和约定版本；存在待核对变化或版本不符时不能验收。页面显示当前／已采用版本、影响来源、旧交付标记和明确核对入口。

第 21 号迁移新增不可变约定历史；`GET /agent/delegations/{id}/requirements` 按约定版本分页，SDK、Module 和远端客户端共用。先前迁移未改写。历史与依赖内容读取检查实际来源，模型每次请求前重查冻结依赖来源，私有附件不能靠依赖引用进入另一个会话。

依据：[依赖契约](../../domainry-agent-sdk/conversation_dependencies.go)、[字段投影](../internal/execution/brief.go)、[依赖事务](../internal/infrastructure/persistence/database/agent/conversation_dependencies_store.go)、[页面版本与历史](../frontend/src/CollaborationRequirements.tsx)、[问题和回复](../frontend/src/PeerMessages.tsx)。

## 验证

- 存储场景：直接／传递依赖、无关字段隔离、旧 worker 拒绝提交、重复变更去重、先核对上游再恢复、传递要求实际进入任务输入、首次执行采用、循环和跨目标拒绝、相同正文不同依赖版本拒绝、23 版历史分页、旧验收成果保留。
- 通信场景：下一轮延后、后续立即消息不被阻挡、过早消费拒绝、问题精确回复与重投、要求变化替代排队消息、系统唤醒来源持久化。应用场景检查私有依赖在接单、模型输入和工具结果读取时被拒绝。
- 真实 Identity／SQLite／HTTP／Chrome：创建独立 Agent、选择工具与成本、委派、真实工具执行、消息、交付验收；再创建依赖约束的任务，改变上游币种约束，查看历史、阻止旧交付验收、提问回复、核对并恢复、重新执行和交付。刷新后保持状态，桌面及 390px 窄屏无横向溢出，页面无 JS 错误。
- SDK 全套、应用／数据库／执行／Module／SaaS 等受影响包全套通过；Peer 场景 race 通过；前端 64 项测试和生产构建通过。Vite 保留既有大 chunk 提示。

可重跑入口：[存储测试](../internal/infrastructure/persistence/database/agent/conversation_dependencies_store_test.go)、[来源测试](../internal/application/conversation_peer_sources_test.go)、[HTTP 夹具](../internal/assembly/web/conversation_collaboration_test.go)、[浏览器脚本](../frontend/tests/collaboration.browser.mjs)。本机截图和结果位于 `/tmp/domainry-peer-c03-browser/`；过程日志 `/tmp/peer-c03-final-*.log`。

## 保留的后续工作

此处的图用于要求有效性，未把它当作完整执行计划或成果依赖管理。通用输入 Schema、转交和不明外部效果的专用核查恢复继续按 C04；语义问题合并、目标统一预算和通信频率继续按 C07／E05；完成条件自动验收与分歧证据核对继续按 C04／E03。本次未记这些条目完成。
