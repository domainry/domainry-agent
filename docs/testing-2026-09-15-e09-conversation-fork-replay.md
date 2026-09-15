# E09 会话分叉与轨迹回放验收

日期：2026-09-15。本轮在已有冻结模型输入、步骤、工具回执、来源审计和会话存储上实现完成边界分叉、受控轨迹读取／导出／对照，以及展示、录制模型夹具和真实分叉三种回放模式。新会话是可单独选择 Agent、接收输入和管理的平权会话，来源字段只用于追溯与继续使用历史快照时的授权复核。E09 验收完成后，完整清单进度为 **15／25**。

## 公开契约与所有权

- [SDK 契约](../../domainry-agent-sdk/conversation_trajectory.go)增加可选 `ConversationTrajectoryService`、来源引用、完整模型请求／响应、工具调用／结果、确定性摘要、导出、回放和对照类型；原 `ConversationService` 及现有 Binding 不增加必需方法。
- 五个路径由 [SDK HTTP 清单](../../domainry-agent-sdk/conversation_http.go)唯一声明，Module 和 SaaS 共用 Agent Adapter。`trajectory_compare` 是读取；创建分叉和 `live_rerun` 是幂等写入。产品 capability 单独发布 `agent.conversation_trajectories`，避免把新增操作塞进过大的通用会话分类。
- 工具定义继续使用 `domainry-tools-sdk.Definition`。Tools 负责可复用的定义、Schema、动作键、效果、幂等和并行声明；Agent 只把执行时冻结的完整公开定义放进自身轨迹。分叉、模型请求和工具回执属于 Agent 会话账本，因此轨迹读取与分叉接口由 Agent 实现，不在 Tools 再建一套执行存储。
- Migration 34 增加 `_agent_conversation_forks`。表内保存来源运行、完成事件边界、轨迹摘要和服务端历史种子；会话对外只返回来源 ID／边界。删除子会话会删除种子，不删除或改变来源运行。

## 稳定边界与独立分叉

- 来源必须是普通会话中已经 `completed` 的准确运行，且具有同一运行的最终 assistant 消息、完成时间和持久事件序号；运行中、失败、取消、等待状态、后台任务或委派内部会话都不能直接分叉。
- 分叉写入在一个数据库事务内重新核对来源完成边界，按 `client_id` 幂等创建新会话及私有种子。相同键改变标题、目标 Agent 或种子返回冲突；不稳定来源不会残留半个子会话。
- 子会话不继承 `active_run_id`，创建后不会调用模型或工具。用户发送新输入时，服务重新生成并授权来源轨迹，核对边界序号和摘要，再把历史快照放入新模型输入；目标 Agent 使用自己的当前冻结配置。
- 历史工具消息被改写成带明确说明的普通历史数据：旧 call 不再是可执行的 assistant tool call，旧 result 不再以 tool role 进入模型。新运行仍可自行规划新的工具调用，但不会因历史记录自动重复旧效果。

## 轨迹内容与三种回放

- `display` 返回模型实际看到的消息、模型身份／推理档位、完整公开工具定义和 Schema、上下文窗口／来源版本／压缩边界、录制响应、工具调用／结果及每项摘要。Provider 原生续接状态、授权证据、确认材料和凭证不进入契约。
- JSON 导出使用固定结构和字段顺序，响应带内容摘要及下载名称。页面可展开每次请求与响应、导出 JSON、核对录制夹具，并按会话／运行 ID 对照请求、响应和工具摘要。
- `model_fixture` 按保存顺序返回一一对应的录制模型响应和工具结果，用于测试执行循环；它不连接真实 Provider，也不调用工具。当前粒度是完整模型调用结果，不声称复现 Provider 私有状态或原始网络分块。
- `live_rerun` 创建上述独立分叉并返回 `ready_for_input=true`、`effects_executed=false`。它不是立即重新执行；页面切到新会话并等待用户输入。

## 授权与副作用边界

- 查看、导出、回放、对照和分叉先检查当前会话的执行读取／管理权限，再走既有来源图审计。工具结果、注册上下文、跨 Agent 发布和私有资料继续使用原有当前授权，不因保存过一次或来源是本人而跳过。
- Integration 使用真实写工具完成两次模型步骤，只产生一次效果；展示和夹具回放、分叉创建及子会话首次新输入都没有重复旧写调用。把来源工具权限撤回后，五种入口全部返回 `tool_access_denied`，被拒绝的分叉没有写入新会话。
- 轨迹对照会分别授权左右两条来源；知道 ID 或摘要不授予读取另一条会话。私有 fork seed 只按 runtime／workspace／user owner key 读取，其他主体得不到存在性信息。

## 页面与部署验收

- [真实 Chrome 脚本](../frontend/tests/conversation-trajectory.browser.mjs)操作编译后的页面、真实 Identity HTTP 和 SQLite：创建来源运行、展开模型可见输入、核对录制夹具、下载并解析 JSON、自对照、创建分叉、点击来源追溯、发送新输入和对照两条轨迹。
- 测试宿主记录模型调用次数。来源完成后为 1；查看、导出、夹具核对、自对照和分叉后仍为 1；只在子会话收到用户新输入后变成 2，并确认新请求包含来源用户输入、最终回复、历史边界说明和当前输入。
- 桌面与 390px 页面无横向溢出，脚本记录 0 个 JavaScript 错误、console error、warning 和失败资源。Module 与 SaaS 浏览器 Adapter 都验证五个路径，SaaS 远程客户端可传输导出字节和回放／对照结果。

## DeepSeek Harness 对照

固定版本 `c291e7961a515f6d7af9304e7fd1d257929aef26` 的 [Session 文档](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/docs/subsystems/session.md)要求 fork 只发生在 turn 间稳定边界并复制所选事件前缀；[LLM replay](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/test-support/llm-replay/README.md)是测试用、无 key 的录制响应适配器，不负责录制，也不访问真实 Provider。本实现采用相同的稳定边界和离线模型响应原则，并结合现有产品权限补了来源实时复核、工具副作用隔离、Module／SaaS 接口和用户可见对照。固定原文及 SHA-256 已归档到[证据目录](evidence/2026-09-15-e09-conversation-fork-replay/deepseek/)。

SDK、实际产品包、完整 Integration／Web、专项 race、91 项前端测试、生产构建和真实浏览器结果见[验收命令与日志](evidence/2026-09-15-e09-conversation-fork-replay/commands.md)。
