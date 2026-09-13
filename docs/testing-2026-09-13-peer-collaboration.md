# 平权 Agent 协作首批交付记录

日期：2026-09-13。当前工作区直接开发，涉及 `domainry-agent` 和相邻 `domainry-agent-sdk`。本批交付可运行的本部署内协作基础；不代表 [25 项核心 TODO](agent-core-capabilities-todo.md)全部完成。

## 使用入口

工作空间侧栏新增“Agent 协作”。在“Agent 目录”创建 Agent，选择已部署模型和获准使用的工具；可以直接开始该 Agent 的对话，也可从当前会话发起委派。委派填写目的、版本化目标、交付物、完成条件和必要输入，接收方在独立会话中执行。

“委派与进度”显示双方、需求版本、执行状态和沟通。可以查看当前会话发出／接收的委派，筛选“需要我处理”，打开实际运行的工具参数、结果、审计、用量及成果。运行详情使用既有 SSE 和交互卡片；工具操作确认仍保持原有精确范围。已接单范围内普通 Agent 消息与交付自动流转，不把通信当作新增业务写入授权。

更新需求时，版本检查、委派状态和旧运行停止在同一事务中提交；继续时创建采用新需求的运行并扣除已用步骤／工具次数。仍有未知写入时，不创建新运行重复执行，先通过原运行核查回执。交付可以声明输出 JSON Schema，格式校验通过后仍需发起方核对证据并验收。

## 实现位置

- [SDK 协作契约](../../domainry-agent-sdk/conversation_collaboration.go)、[协作工具](../../domainry-agent-sdk/conversation_collaboration_tools.go)、[HTTP 契约](../../domainry-agent-sdk/conversation_http.go)及 [持久化端口](../../domainry-agent-sdk/persistence/conversation_collaboration.go)。新增 `agent_list`、`agent_delegate`、`delegation_get`、`agent_message`、`delegation_update`，并接入 Module／SaaS 的动作清单、能力描述、HTTP 和远端客户端。
- [按 Agent 执行与冻结](../internal/application/conversation_agents.go)、[委派服务](../internal/application/conversation_delegations.go)、[消息消费](../internal/application/conversation_peer_inbox.go)、[协作工具宿主](../internal/application/conversation_collaboration_tools.go)。`ConversationOptions.AgentModels` 是宿主模型注册表，用户只选择注册键；部署既有工具范围和当前授权仍是上限，接收方不继承发起方 Agent 的工具选择。
- [迁移 20](../internal/infrastructure/persistence/database/agent/conversation_collaboration_schema.go)新增 Agent、委派、消息和幂等回执表，并接入用户生命周期。任务和运行复用原有队列、租约、fence、交互与审计机制。协作效果事务对来源运行作 CAS，取消或接管后旧执行者不能继续提交新效果。
- [协作页面](../frontend/src/CollaborationDialog.tsx)与 [运行详情](../frontend/src/RunDialog.tsx)。没有新增父／子 Agent 对象；来源关联用于责任、证据、循环检查和限额。

## 验收证据

[存储测试](../internal/infrastructure/persistence/database/agent/conversation_collaboration_store_test.go)覆盖 owner／工作区隔离、接单重投、独立会话、消息原子消费、重启后完成通知、旧运行写入被拒绝、需求更新后新运行／剩余预算、并行上限、循环限制，以及超过一页暂停消息不阻挡后续可运行消息。

[HTTP 与执行测试](../internal/assembly/web/conversation_collaboration_test.go)使用真实 Identity、持久化和模型夹具，验证不同模型注册键、接收方指令／工具、注册表外部修改不影响已装配服务、实际消息／交付工具调用、配置更新后拒绝使用新配置唤醒旧委派、格式错误拒绝、验收与重复提交。[私有来源边界测试](../internal/application/conversation_peer_sources_test.go)验证消息和交付读取均不能隐式跨会话分享私有附件。撤回实际使用过的工具权限后，消息、任务预览和交付均不继续提供该来源内容。

[浏览器脚本](../frontend/tests/collaboration.browser.mjs)通过 Chrome 操作实际构建的前端和上述宿主，验证创建 Agent、发起委派、协作流转、实际执行、验收、刷新恢复，以及 390 px 窄屏下弹窗边界／横向溢出。截图检查覆盖执行详情、已验收状态和窄屏页面；执行夹具用于验证链路，不作为真实模型任务成功率对照。

本次已通过 Agent 全量 `go test ./...`、SDK 全量测试、前端 61 项测试及构建；最终相关改动另做定向回归。竞态检查覆盖新增存储与 HTTP 场景。构建仍有既有大型静态资源分块提示。

复验命令：

```sh
go test ./...
go test -race ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run '^TestPeer' -count=1
npm --prefix frontend test
npm --prefix frontend run build
```

SDK 测试在相邻 SDK 目录使用当前 `go.work`。浏览器复验先构建前端，设置 `AGENT_PEER_BROWSER=1`、`AGENT_NODE_BINARY`、`AGENT_PLAYWRIGHT_MODULE`、独立的 `AGENT_UI_TEST_OUTPUT`，运行 `go test ./internal/assembly/web -run '^TestPeerCollaboration' -count=1`。

## 当前边界

- Agent 配置修改／停用后，旧快照不会悄悄采用新配置；需要按当前配置重新委派。默认 Agent 来自部署配置，页面不改写该部署配置。
- 首批是同一当前用户权限范围内的不同 Agent，不提供跨用户共享授权；专业工具的选择可以不同，但来源结果仍检查当前用户的实际权限。
- 每个来源会话及其转委派链最多 32 条委派，防止自委派／循环及过深委派；每条委派的消息数量和步骤／工具次数有界。尚无整个目标统一的 token、累计时间与费用账本。
- API 返回有界记录；协作工具返回消息及大输入的摘要时带完整性标记。完整原始执行通过运行详情和结果读取入口查看。
- 有委派引用的会话目前可归档，直接删除会拒绝，避免留下仍可执行的任务或失去来源的结果；用户整体生命周期仍覆盖新表。
- 尚未实现自动任务依赖图、跨任务需求传播、自动完成条件验收、分歧证据核对和主对话内联进度。E03–E09、MCP、Code Mode、动态 Skill、图片、业务事件、外部 Agent 协议及完整编码环境保留在原 TODO。
