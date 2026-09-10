# 持久会话（Conversation v1）

会话支持可选的 [文档检索](knowledge.md)，以及 `conversation.execution.v1` 工具执行扩展。当前 Web Module 已装配时间、计算、历史检索、原文读取、个人记忆与待办工具，以及 `ask_user` 和持久化确认 / 核查流程；工具按权限与已挂载能力开放。会话附件已接私有原文件和管理网页，尚未接知识索引，见 [附件验证](testing-2026-09-10-attachments-web.md)。Runtime 宿主已接业务目录、记录、关联、动作及流程；其他资源的授权范围、共享资料库与邮件等仍在 [能力清单](agent-capabilities-todo.md) 中。纯文本模型继续兼容，各项验收状态以清单及对应记录为准。

## 和原有能力的区分

| 能力 | 用途 | 入口 / 存储 |
| --- | --- | --- |
| Interactive | 单次业务理解、候选路由、结构化结果 | 原有 `/agent/runs`、`/agent/sessions` 和 Interactive 表 |
| Conversation | 用户持续聊天、历史上下文、个人记忆、摘要、运行恢复 | 新 `/agent/conversations` 和专用表 |
| Task | 后台业务任务和受控工具执行 | 原有 TaskRunner、任务表和 Host Ports |

Conversation 不调用 InteractiveRunner，不依赖外部 Provider 保存 session。纯文本路径发送我们组装的 `system/user/assistant` 消息数组；配置工具宿主后，通过可选 `ConversationAgentModel` 多次发送模型步骤和实际工具结果。工具参数完整结束并校验后才执行。凭证、授权策略和 Provider 原生续接状态不会作为工具参数或浏览器公开数据。

SDK 提供可选的 `ConversationBinding.Conversations()`，不在旧 `Binding` 上增加必需方法。SDK 持有类型、HTTP Action 清单、OpenAPI 和 Repository 接口；Agent 持有应用服务、SQL、后台执行和传输实现。Runtime 从 Binding 获取并挂载新增的 `conversations` HTTP Adapter。

## 存储与隔离

Migration 1、2 保持原样；Migration 3 增加 `_agent_conversation_steps` 和 `_agent_conversation_tool_calls`，同样在原有 `_schema_migrations` 中登记。Module 借用宿主数据库，SaaS 使用自己的 Agent 数据库，均通过现有 ORM 生成 SQL。步骤输入、模型指纹及原生续接块保存在内部记录；Run 的 `steps` 只返回有界的公开执行视图。

Migration 4 增加 `_agent_conversation_interactions`，保存问题、确认、核查记录及幂等答复。相同调用的确认与核查分别保存，核查不会覆盖原来的批准凭据。迁移 1–3 的内容不变。

Migration 5 增加 `_agent_user_todos` 和 `_agent_todo_mutations`。待办按 runtime / workspace / user 保存，记录批次与原始项次；第二张表保存网页操作的幂等回执。工具操作复用已有调用账本，同事务写入待办、执行结果及事件。迁移继续通过宿主唯一 `_schema_migrations` 账本执行，迁移 1–4 不变。删除来源会话不会删除个人待办或网页操作回执；来源引用不授予查看原会话的权限。

| 表 | 保存内容 |
| --- | --- |
| `_agent_conversations` | 标题、归档标记、记忆开关、修订号、消息序号、当前 run、当前摘要 |
| `_agent_conversation_messages` | 不经摘要覆盖的原始用户消息和完整回复 |
| `_agent_conversation_runs` | 幂等键、请求摘要、运行状态、尝试次数、租约、fence、当前尝试的草稿、字节数、事件游标、用量和错误码 |
| `_agent_conversation_inputs` | 回复请求实际发送给模型的完整输入快照，用于恢复和重试 |
| `_agent_conversation_summaries` | 结构化摘要及版本、前一摘要、覆盖序号、来源哈希和模型 |
| `_agent_conversation_events` | 已提交的运行事件、文本增量和单调递增序号 |
| `_agent_user_memories` | 用户显式保存的个人偏好，支持修改、停用和删除 |
| `_agent_conversation_steps` / `_agent_conversation_tool_calls` | 冻结的模型步骤、原生续接块、工具调用与结果账本 |
| `_agent_conversation_interactions` | 绑定 run / step / call 的等待事项、工具版本 / 参数摘要、答复和有效期 |

所有查询使用 `owner_key = SHA256(JSON([runtime_id, workspace_id, user_id]))` 隔离，子记录同时匹配 conversation ID。哈希是隔离键，不是内容加密。角色改变不清空个人会话。HTTP 身份来自宿主认证上下文，不能从 JSON 或查询参数指定其他用户。

SaaS 的一个 API key 对应配置的一个 `AGENT_SAAS_RUNTIME_ID`；请求中的 Runtime ID 必须匹配。浏览器通过 Runtime 的产品接口访问，不能持有 SaaS service key 或自行发送授权身份。当前未实现一个 SaaS key 服务多个 Runtime 的映射。

删除会话使用修订号，并在事务内删除其消息、run、输入快照、摘要、事件、步骤、调用及交互记录。个人记忆独立删除。归档可恢复，不清除消息；此版不做会话历史自动清理，也不会把原有 `agent.dialog.v1` 清理策略套到新会话上。交互有效期只关闭待处理事项，不删除历史。

## 执行、幂等和恢复

创建会话必须提供 `client_id`；发送消息必须提供 `client_message_id`。同一个键和同一内容返回原记录，同一个键换内容返回 409。发送在一个事务里写入用户消息、queued run、事件及会话的 active run。

每个会话同时只有一个活动 run，新消息冲突返回 `agent.conversation.busy`。不同会话可并发。工作线程用数据库领取 queued 或租约过期的 running run；每次领取递增 fence，定期续租。完整回复、终态事件和清除 active run 在一个事务内提交。

通常状态为 `queued → running → completed / failed / cancelled`。工具运行也可进入 `waiting_user`、`waiting_confirmation` 或 `needs_reconciliation`。等待释放租约、增加 fence，保留会话的 active run；迟到的 worker 无法继续提交结果。取消同样立即增加 fence。failed/cancelled 只有在仍为最新处理且交互未被拒绝、取消或过期时才能 resume；已核查或仍待核查的外部操作按账本恢复，已完成操作不重放。completed 不能 resume。

`ask_user` 必须独占一个模型步骤，答复保存为原始用户消息，同时配对原工具调用的结果并作为 user 消息交回模型；模型看到补充或纠正后才能规划后续步骤。浏览器通过 `POST /agent/conversations/{conversationID}/runs/{runID}/respond` 提交 `interaction_id`、`client_id`、`expected_revision`、`decision`，回答另带 `answer`。decision 分别为 `answer`、`approve`、`reject`，参数不能在确认请求中修改。同一答复重复提交复用结果，不追加第二条消息；改变答复返回冲突。

答复入口使用独立的 `agent.conversations.respond` 权限；批准 / 回答前检查工具版本和当前权限，worker 继续执行前再检查一次。批准凭据由服务端加载，浏览器和模型不能自行提交凭据。宿主使用可选 `ConversationInteractionAuthorizer` 决定答复权限。问题 / 确认默认有效期 24 小时，`ConversationOptions.InteractionTTL` 可调整，最长 7 天；独立清理循环及提交入口都检查有效期，过期后 run 失败并解除会话占用。核查记录不自动过期，通过 resume 查询实际结果，通过 cancel 停止处理。

回复调用之前冻结输入快照。进程重启、租约恢复和手动 resume 都复用该输入及幂等键，即使个人偏好已经修改。冻结之前中断则重新组装；已提交摘要可继续使用。模型服务是否支持幂等由模型服务决定：异常恢复可能重复产生模型费用，但 fence 防止重复提交回复。纯文本路径恢复时使用当前模型配置；工具路径还会核对步骤保存的模型配置指纹，变化时拒绝直接续接。

默认每个服务实例 2 个工作线程、30 秒租约、每 10 秒续租、1 秒检查队列、每轮最多 5 分钟。关闭服务会取消本地请求，保留未完成 run 供租约到期后恢复。

## 历史上下文与压缩

上下文组成：固定系统指令 → 开启的显式个人偏好 → 最新摘要 → 摘要覆盖范围以后的原始消息 → 当前用户消息。

默认按序列化消息数组的 **UTF-8 字节数** 控制输入预算（64 KiB），不把字符数冒充精确 token 数。接近 70% 时尝试压缩旧消息，尽量保留最近四轮及当前问题。短历史在硬预算内可直接回复。

压缩只截止于完整的 assistant 消息，不压缩当前用户问题。每次摘要包含：

```json
{
  "goal": "当前目标",
  "constraints": ["用户约束和纠正"],
  "facts": ["已知事实及具体标识"],
  "decisions": ["已确定的决定"],
  "open_items": ["仍待处理的问题"]
}
```

摘要指令要求沿用仍有效的事实、约束、决定和未完成事项，仅按新的明确证据更新或完成，并采用用户最新的纠正。旧摘要与一段完整历史一起生成新摘要；记录 `previous_id`、`through_seq`、输入哈希和模型。后续组装从 `through_seq` 后继续，避免同时重复注入旧原文。长历史分批处理，每批最多读取 1,000 条、整轮最多 256 次压缩，并受执行超时约束。

摘要必须是结构匹配、非空、长度合格的 JSON；兼容模型将其包在单个完整的 Markdown JSON 代码块中，去掉外层后仍严格校验字段、类型和尾随内容。不会从说明文字或多个代码块中猜取 JSON。无效摘要不会替换当前摘要。摘要失败会把本轮标记为 `context_failed`，原始消息仍在，可修复模型配置后 resume。没有足够空间且无法压缩完整历史时也会明确失败，不静默丢弃用户输入。原文可通过消息接口分页读取；有对应权限时，模型也可使用 `history_search` / `history_read` 核对原始消息。

个人记忆不是聊天模型自动写入的。网页设置或调用方显式调用记忆 API，用户可以审阅和删除；每位用户每个 workspace 最多 32 条，每条标题 128 字节、内容 512 字节。新会话默认 `memory_enabled=false`，开启后注入 enabled 的偏好。关闭开关只影响未来组装，已写入历史的内容不会被追溯移除。

## 当前工具目录与连接开关

每一步先由已挂载的 `ConversationToolHost.ConversationTools` 按当前权限生成目录，再校验工具版本、输入／输出 Schema、动作权限键、读写影响、超时、结果字节上限和幂等方式。目录按工具键排序并复制为独立快照；宿主后续修改注册缓冲区不会改写已保存的模型输入。

宿主可通过 `ConversationOptions.ToolAvailability` 绑定 SDK 的 `ConversationToolAvailability`。直接工具宿主、普通 Module 宿主或延迟装配的 `ConversationApplicationHost` 实现该可选接口时也会自动接入；显式选项优先。回调接受服务端的 runtime／workspace／user 身份及已注册工具键，工具未启用、连接断开／过期或状态未知时返回 `false`；不需要连接的本地工具返回 `true`。回调仅检查状态，凭证刷新与账号管理属于后续 F01。

检查覆盖新步骤目录、冻结步骤恢复、实际调用前以及业务／知识历史来源复核。目录检查共享 5 秒上限，实际调用前检查也有 5 秒上限，宿主须响应 Context。连接检查发生故障时返回统一 `tool_availability_failed`，内部错误文本不进入模型、历史或网页。目录变化不会替换冻结输入；连接恢复后可继续原步骤，已完成工具结果按原幂等记录复用。没有该可选策略的旧宿主继续自行通过目录和具体动作授权负责可用性；这不代表系统自动探测任意远端服务健康状况。

实现与实际 Identity／HTTP／浏览器证据见 [A03 工具目录验收](testing-2026-09-10-tool-catalog.md)。

## 产品接口

全部路径由 SDK 清单生成授权元数据及 OpenAPI。

| 方法 / 路径 | 行为 |
| --- | --- |
| `POST /agent/conversations` | 创建或按 client_id 重放 |
| `GET /agent/conversations` | 按最近更新时间排序；search、include_archived、before_id、limit |
| `GET /agent/conversations/{id}` | 详情，包含 active_run_id 和 revision |
| `PATCH /agent/conversations/{id}` | expected_revision + title / archived / memory_enabled；运行中拒绝修改 |
| `DELETE /agent/conversations/{id}?expected_revision=N` | 删除会话及子记录 |
| `POST /agent/conversations/{id}/messages` | 接受一条用户消息，返回 202 和 run |
| `GET /agent/conversations/{id}/messages` | 分页获取原始消息 |
| `GET /agent/conversations/{id}/runs/{runID}` | 查询运行状态及错误码 |
| `GET /agent/conversations/{id}/runs/{runID}/events` | after_seq、limit 读取事件页 |
| `GET /agent/conversations/{id}/runs/{runID}/events/stream` | SSE 重放和跟随已提交事件 |
| `POST /agent/conversations/{id}/runs/{runID}/cancel` | 取消 |
| `POST /agent/conversations/{id}/runs/{runID}/resume` | 恢复最新失败/取消的 run |
| `POST /agent/conversations/{id}/runs/{runID}/respond` | 回答绑定的问题，或批准 / 拒绝具体操作 |
| `GET /agent/conversations/memories` | 个人偏好列表 |
| `PUT /agent/conversations/memories/{memoryID}` | 新建 expected_revision=0；修改使用现有 revision |
| `DELETE /agent/conversations/memories/{memoryID}?expected_revision=N` | 删除偏好 |

列表默认 50、最多 100。会话列表的 `next_cursor` 原样作为 `before_id`，不解析为会话 ID；并发更新期间属于实时列表，不提供快照分页保证。消息默认取最新一页并按序号升序返回，用 `next_before_seq` 继续向前翻。指定正数 `after_seq` 则从该序号之后按升序增量读取，用 `next_after_seq` 翻页，避免跳过中间消息。

SSE 的 `id` 是 run 内事件序号。断线携带 `Last-Event-ID` 或 `after_seq` 重连；连接最多 30 秒，每 250ms 读取一次新事件。事件包括 `run.queued`、`run.started`、`context.compacted`、`message.delta`、`run.completed`、`run.failed`、`run.cancelled`。终态事件里的 assistant_message_id 对应消息接口的完整回复。流只读，不重新触发模型；关闭浏览器不取消 run。

支持 `conversation.stream.v1` 的模型在生成过程中持续提交 `message.delta`，然后由 SSE 推送；这不是等待完整答案后切片。一个文本片段和 run 草稿在同一事务提交，成功后才继续读取模型。事件 data 示例：

```json
{"attempt":1,"offset":0,"text":"你好"}
```

`offset` 是当前 attempt 内的 UTF-8 字节偏移，不是 JavaScript 字符串长度。Run 快照包含 `draft_text`、`draft_bytes`、`last_event_seq` 和 `attempt`。刷新时用快照显示草稿，从其 last_event_seq 续订；相同或更旧事件序号去重，同一 attempt 的增量只在 offset 等于本地 UTF-8 字节数时追加，否则重新获取快照。`run.started` 的 `draft_reset=true` 表示新的 attempt 开始，清空旧草稿；resume 的 queued 事件也会标记草稿重置。run 的事件序号跨 attempt 继续递增。

只有模型明确正常结束、最终文本与草稿一致，才原子写入正式 assistant 消息并清空 run 草稿。失败或取消的草稿保留供查看，永远不注入后续上下文。resume 或租约恢复重新生成整条回复，清空上一 attempt 的草稿；不宣称能从模型中断的 token 继续生成。收到 completed 后从消息接口读取正式回复。客户端自行区分旧失败草稿与新的正式回答。

发送示例：

```json
{"client_message_id":"browser-generated-uuid","message":"继续刚才的讨论"}
```

刷新网页：先取会话详情及消息；如果 active_run_id 非空，查询该 run 并续订事件。发送失败重试时保留原 client_message_id，不生成新键。

## 模型配置与联调

SaaS 可只启动 Conversation，无需设置旧的 `AGENT_HTTP_*`。后者仍只用于 Interactive / Task；只要设置了任何旧 Provider 参数，就必须完整配置且通过校验。

模型网关示例（密钥在本地环境或部署密钥管理中配置）：

```sh
export AGENT_CONVERSATION_PROVIDER='gateway'
export AGENT_CONVERSATION_BASE_URL='https://models.example.com' # 替换为实际模型服务地址
export AGENT_CONVERSATION_PROTOCOL='chat_completions'
export AGENT_CONVERSATION_MODEL='glm-5.3-flash-free'
# 配置 AGENT_PROVIDER_API_KEY；也可以使用 AGENT_CONVERSATION_MODEL_API_KEY，后者优先。
# 配置 AGENT_SAAS_API_KEY，作为 Runtime 访问 Agent 的 service key。
export AGENT_SAAS_RUNTIME_ID='your-runtime-id'
export AGENT_SAAS_DB_DRIVER='sqlite'
export AGENT_SAAS_DB_DSN='agent.db'
export AGENT_SAAS_ADDRESS=':8090'
go run ./cmd/domainry-agent
```

根据已接入服务的接口约定，支持以下协议。服务地址通过部署配置传入，不绑定具体服务商。模型必须出现在所选协议的目录中且属于 API key 的 scope；目录可变，因此不硬编码模型名称或按前缀猜协议。

| AGENT_CONVERSATION_PROTOCOL | 相对路径（拼接配置的服务地址） | 输出 token 参数 | 目录示例（非完整列表） |
| --- | --- | --- | --- |
| `chat_completions`（默认） | `/v1/chat/completions` | `max_completion_tokens` | `glm-5.3-flash-free`、DeepSeek、Kimi |
| `messages` | `/v1/messages` | `max_tokens` | Claude、Gemini、MiniMax |
| `responses` | `/v1/responses` | `max_output_tokens` | `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna` |

`gateway` 模式使用 `x-api-key`，Chat 使用 `max_completion_tokens`；`AGENT_CONVERSATION_BASE_URL` 配置服务源地址并按所选协议拼接路径。`AGENT_CONVERSATION_MODEL_URL` 可覆盖完整端点。Messages 把前置系统消息合并到顶层 system；Responses 每次传完整 input，并设置 store=false，不传 previous_response_id 或 Provider conversation。纯文本及摘要路径不传工具；工具路径按对应协议传定义、调用和结果。签名 / 加密推理续接块只存内部步骤，浏览器只收到文本及明确的工具视图。

通用兼容端点可设置 `AGENT_CONVERSATION_PROVIDER=compatible`（默认），此时必须提供 MODEL_URL 或 CONVERSATION_BASE_URL。Chat/Responses 使用 Bearer，Messages 使用 x-api-key 与 anthropic-version。兼容 Chat 使用 max_tokens，gateway Chat 使用 max_completion_tokens。已有仅实现 `GenerateConversation` 的注入模型仍可使用，但不公布 stream 能力。

回复请求使用 stream=true；压缩摘要使用 stream=false。聊天流要求正常 stop 和 `[DONE]`，Messages 要求 end_turn 和 message_stop，Responses 要求 response.completed、completed 状态及最终文本匹配。遇到工具调用、截断、异常 EOF、格式错误、空回复或超限会失败，不自动发第二次请求重试。SSE 事件参考 [OpenAI Responses](https://developers.openai.com/api/reference/resources/responses/streaming-events) 和 [Anthropic Messages](https://platform.claude.com/docs/en/build-with-claude/streaming)。

Provider 默认输出 4096 tokens、HTTP 总超时 120 秒；应用层回复最多 8 KiB、摘要最多 4 KiB、单条用户消息最多 16 KiB。模型窗口需容纳输入预算、输出及协议开销，字节预算与 token 上限独立。取消本地 run 会关闭模型 HTTP 请求；其他 Agent 实例收到取消后最迟在续租失败时关闭请求。不能保证上游停止计费，已接入服务的文档未承诺请求幂等或独立取消端点。

Module 通过 `module.Options.ConversationProviderName`、`ConversationProtocol` 选择协议，或通过 `ConversationProvider` 注入无状态模型；`module.ConversationOptions` 调整预算和 worker 参数。环境配置由 `module.OptionsFromEnvironment()` 统一加载。

未配置模型时仍可管理会话和记忆，发送返回 503 `model_not_configured`，SaaS `/readyz` 也返回 503。配置错误（如 gateway 缺地址 / key / model）会阻止启动。readiness 检查配置、worker 生命周期及会话表可读性，不发付费模型请求，不代表上游实时健康。SaaS Descriptor 仅公布已挂载能力；Conversation-only 的 remote Binding 不启动业务 worker、不挂载业务 Adapter。静态 capability catalog 仍描述源码支持的契约，并与 Runtime 做摘要校验。

本次 SDK 与 Agent 同时修改。当前 `go.work` 引用当前仓库与相邻的 `../domainry-agent-sdk`、`../domainry-connectors` 和 `../domainry-identity`，用于本地编译联调；没有添加 go.mod replace。发布消费前需先发布新的 SDK 标签、更新 Agent 的 SDK 依赖，再发布 Agent 并升级宿主依赖；相关源码依赖的新版本仍待发布。

验证覆盖 SQLite 持久化重开（包括草稿）、并发幂等、跨身份隔离、取消和租约 fencing、输入冻结、摘要失败恢复、原文保留、Conversation 独立启动和 readiness、三种协议的模拟 HTTP 联调、首段早于完成、Module/SaaS 到浏览器 SSE、断线重连、取消上游请求与 UTF-8 字节偏移。PostgreSQL/MySQL 的迁移通过 ORM 方言检查；尚未连接真实 PostgreSQL/MySQL；外部模型真实验收范围见下文。

真实模型验收需先配置网关地址及 API key，再显式执行以下命令（会发送三次模型请求，可能消耗额度；日常 go test 默认跳过）：

```sh
AGENT_CONVERSATION_LIVE=1 go test ./internal/infrastructure/provider -run '^TestGatewayLiveConversation$' -count=1 -v
```

该测试验证真实首段回调、带历史的第二轮回复、结构化摘要；不会输出 key 或原始模型响应。它不替代数据库和浏览器链路测试。2026-09-09 已使用临时注入的 key，通过 glm-5.3-flash-free 的 Chat Completions 真实验收：流式回复、带历史的第二轮回复、结构化摘要，共三次请求。Messages 和 Responses 仍仅完成模拟协议验收。详见 [测试记录](testing-2026-09-09.md)。

## 日常会话体验补齐（2026-09-09）

- 浏览器输入草稿按本地测试身份范围和 conversation ID 写入 localStorage；切换、刷新后恢复，发送被服务端接受后仅清理该条草稿，删除会话成功后清理其本地草稿。尚未发送的内容不进入服务端消息历史。
- 发送结果不确定时保留同一逻辑发送的 client_message_id，刷新后重试同样文本继续使用该标识。迟到的确认不会清除另一标签页中更新的草稿。浏览器存储被禁用或容量不足时退回当前页面内存，并明确提示无法跨刷新恢复。
- 当前草稿 namespace 为 local-playground，适配固定本地测试身份。未来正式用户入口须注入实际用户/工作区范围，不能把本地测试范围用于多用户入口。
- 前端接入现有标题 search 查询、分页和归档过滤，搜索不更改正在查看的会话。个人记忆支持编辑并保留启用状态；可从完整消息打开预填表单，经用户确认保存。超过 512 字节时要求提炼，不自动截断。
- 模型 HTTP 401/403、402、429、408/504、其他 5xx 分别转成访问拒绝、额度/账单限制、限流、超时和服务不可用的稳定错误码；网络异常单独分类。不保存上游错误正文、URL 或凭证。429 不等同于余额不足。
- Run 持久化使用白名单保留安全错误类别，未知错误继续使用 provider_failed/context_failed。浏览器将错误码翻译成中文处理建议；生成中断的草稿仍保留。流式连接恢复后清除该连接的临时错误提示。

真实长会话验收使用默认 65,536 字节预算和独立 SQLite 数据库，覆盖多次自动摘要替换、用户纠正、未完成事项、完整原文分页及服务实例与数据库重开，见测试记录。该测试通过 `AGENT_CONVERSATION_LONG_LIVE=1 go test ./integration -run '^TestGatewayLiveLongConversation$' -count=1 -timeout 10m -v` 显式开启，会调用真实模型；日常测试默认跳过。


## 对话中的个人记忆操作

`memory_search` 查询个人记忆；`memory_save` 新增、更新或启停；`memory_forget` 删除。
只有已授予相应 Identity 权限且宿主装配本地原子执行存储时，写工具才会进入模型目录。
模型只能选择工具与参数，不能提交授权或 worker 凭据。

消息发送可携带如下字段，明确允许本次请求管理当前用户自己的个人记忆：

```json
{
  "client_message_id": "request-unique-id",
  "message": "记住：周报按项目组织",
  "write_scope": { "personal_memory": true }
}
```

默认没有这项范围。未提供范围时，具体记忆操作暂停并展示确认；提供时直接执行范围内的操作，
仍逐次检查 Identity 权限和期望修订号。同一 client_message_id 的重试必须保持原消息与原范围；
范围随本次 Run 冻结，不能通过恢复接口修改，不会继承到下一条消息。
网页开关与“使用个人记忆”不同：前者授予本次修改范围，后者决定是否把已启用记忆纳入上下文。

本地变更与工具结果、执行事件同事务提交。记忆内容与原始对话分别保存；删除记忆不会删除曾经
包含这些内容的历史消息。具体版本、来源记录保留与用户数据清理仍按能力清单继续完善。

## 个人待办

`todo_create` 一次创建 1–20 项并返回同一批次的稳定序号；`todo_list` 查询，`todo_get` 读取完整事项；
`todo_update` 修改或将状态设为 `completed` / `open`；`todo_delete` 删除。完成或删除前一项后，
原来的第二项仍是 `position=2`。多个批次存在歧义时通过 `ask_user` 等待真实用户选择。

消息可传 `"write_scope":{"personal_todos":true}`，允许本次请求管理当前用户自己的待办；
未提供时具体写入等待确认。它与 `personal_memory` 分别保存，彼此不能代替，也不能增加 Identity 权限。
网页编辑调用下列同一存储能力的接口，直接操作仍检查对应工具权限，不依赖已配置模型：

| 接口 | 参数与行为 |
| --- | --- |
| `GET /agent/todos` | 可选 query、status、source_conversation_id、batch_id、cursor、limit；查询当前用户的事项 |
| `GET /agent/todos/{todoID}` | 返回完整说明、截止日期、来源与修订号 |
| `POST /agent/todos` | client_id、items，可选 source_conversation_id；批量校验并原子创建 |
| `PATCH /agent/todos/{todoID}` | client_id、expected_revision、patch；修改指定字段，冲突返回 409 |
| `DELETE /agent/todos/{todoID}` | JSON 正文携带 client_id、expected_revision；重复原请求返回原回执 |

创建项必填 title、timezone，允许 description，以及 due_date / due_at 之一。title 最多 128 UTF-8 字节，
description 最多 1024 字节；每个所有者最多 1000 项。due_date 是 IANA 时区内的 `YYYY-MM-DD` 日历日期，
不隐含零点；due_at 是明确 RFC3339 时间，偏移必须匹配该时区。修改其中一个截止字段会清除另一个，
空字符串用于取消截止日期；只改标题时保留原来的精确时间。

列表按批次创建时间倒序、原始项次正序返回，默认每页 20 项。扫描和响应体均有界；complete=false 时
继续使用 next_cursor，不能把第一页作为全量结果。游标绑定身份与查询；并发修改可能需要刷新。
调用方重试时必须保留原 client_id、参数和修订号，同键改参数返回冲突。网页保存不确定请求，刷新后
可继续核查同一次操作；删除来源会话后，旧创建请求也不会重复创建事项。

真实 Identity、HTTP、SQLite 及模型协议夹具的测试覆盖上述流程；新的记忆 / 待办界面仍待浏览器验收，
实际外部模型的指代理解另行验证。详见 [个人待办验收记录](testing-2026-09-10-tools.md)。

## 工具结果压缩与原始结果读取

在宿主开放并授权 `tool_result_read`、且挂载结果存储接口时，执行器会将较大工具结果转换为
`stored_result_preview`，再冻结下一步输入。完整数据仍在已有工具调用账本，不增加独立结果表。
预览保留实际 status、resource_id、error_code、原结果字节数、引用和真实 JSON 前缀，明确
`content_complete=false`。原始用户消息与答复、assistant 文本、调用参数和 Provider 原生续接块均不改动，
工具调用与结果保持配对。预览不能作为全量统计依据，模型需要读取遗漏的相关内容。

每个结果引用包含 conversation_id、run_id、step、call_id 和完整结果的 SHA-256。读取工具接受引用、
offset 和 max_bytes，返回原始结果 JSON 的 UTF-8 分片、next_offset、total_bytes 和 complete。
max_bytes 默认 4096，上限 8192；遇到大量转义内容或较小上下文预算时，实际分片可以更小。
按 next_offset 连续读取，从 0 开始拼接全部 json_text 后才能重建完整 JSON。最近一步明确请求的
读取结果会保留，避免刚读取的分片再次变成只能读取的预览。

每次读取都验证来源所有者、不可变结果校验值、原工具版本和当前权限；模型下一次请求前再次授权。
即使引用的是另一个已完成会话中的结果，也适用同样规则；对结果读取操作的再次引用会追溯原来源授权，
不能通过多层引用保留已经撤销的数据访问。删除来源会话后，原引用无法继续读取。

默认先将结果预览限制为 8192 字节以内（较小上下文取其四分之一），仍超总预算时进一步缩小旧结果预览。
预算按完整 JSON 包括转义、工具定义、模型配置和步骤包装计算；历史摘要提前为这些内容预留空间。
压缩后的输入及 `context.tools_compacted` 事件同事务保存，事件只包含步骤和字节统计；重启复用相同输入。
这些应用侧元数据不会作为额外的 Provider 协议字段传出。

如果没有结果读取权限、没有存储支持，或保留的用户输入、调用参数和续接数据仍超出预算，则明确失败，
保留已完成操作及完整结果。运行内压缩与跨轮定位已有代码，浏览器和实际模型验收仍待完成；
不能仅以夹具测试视为整批 A07 已交付。验证范围见 [工具阶段记录](testing-2026-09-10-tools.md)。

## 跨轮执行定位与历史运行查看

`history_search` 和 `history_read` 返回原消息的 `run_id`。在宿主开放 `execution_read` 时，
回复上下文增加最多四个最近运行的定位信息，从最近 32 条原始消息中去重取得；仅包含会话、运行、消息 ID 和序号。
这些信息随逐轮输入冻结，独立于模型生成的摘要。摘要旁还保留来源会话与 `summarized_through_seq`，
较早工作通过原始历史搜索恢复定位。资源 ID 和执行结果不会通过这个自动索引预先注入。

`execution_read` 接受实际取得的 `conversation_id` / `run_id`，从原有工具账本分页读取已开始的调用，
默认每页三项，最多五项。返回历史观察到的调用状态、结果状态、资源 ID、错误码；已经完成落库的结果提供
`tool_result_read` 所需的完整引用。尚未执行的模型提议不在账本目录内；`complete=true` 只表示当前分页结束，
不表示请求中的所有工作已经完成，更不表示外部业务资源现在仍处于记录中的状态。

每项调用都检查原工具定义及当前资源权限，再返回任何工具名、状态或资源 ID。不能披露的项省略并标记
`omitted=true`，不能将空目录解释为从未执行过操作。游标绑定所有者、会话、运行、查询规格和事件序号；
运行变化时返回 `execution_cursor_changed`，需重新读取第一页。目录不包含工具原始参数、完整正文、授权证据或
Provider 续接状态；原始结果通过单独读取获取。读取当前正在执行这一调用的 Run 被拒绝，避免自引用。

目录结果保存原调用与观察结果的校验值。目录或完整结果被再次读取、压缩、在下一步模型请求中使用时，
沿来源关系再次授权；嵌套读取不能代替原工具权限。恢复冻结步骤时仍执行这些检查；结果已变化则要求重新读取。
遍历去重并限制最多 256 个记录，防止循环或无限展开。目录读取只读账本，不重新执行、确认或恢复原业务操作。
此功能使用可选的 SDK 存储接口，不新增数据库表；旧账本记录无需补写即可查询。

新完成的工具结果引用与 `tool.completed` 事件、Run 公开快照同事务保存。网页每条消息提供“查看处理记录”，
通过原有 owner-scoped Run 接口加载对应运行，独立订阅非终态运行的 SSE，展示结果节选、等待状态和已保存的回复。
查看窗口不会替换主对话观察的 Run；关闭窗口、切换会话或登录身份会取消相应请求及订阅。
旧版没有结果引用字段的 Run 仍能展示原记录，模型可以通过账本查询取得引用。

## 公共执行规则与旧 Task 的边界

`internal/execution` 只包含无身份、无存储依赖的预算与 JSON 校验规则：

- `Budget.Reserve` 返回预留后的调用数与逻辑成本，拒绝负数和整数溢出，不直接修改运行状态。
  Task 在原有带 fencing 的数据库事务中预留；Interactive 从持久记录检查；Conversation 在遍历已冻结步骤时重建使用量，
  恢复已有步骤不会额外增加逻辑调用。默认调用上限共用 20，具体上限仍取对应运行 / 服务配置。
- Task 的逻辑成本继续使用已有工具权重及 low / normal / high 配置。Conversation 目前只计算调用次数，成本 charge 为 0；
  这些单位不代表模型 token 或实际账单金额。
- JSON Schema 编译与校验由 Task 输出和 Conversation 工具共用。Task 输出从只检查必填字段升级为完整契约校验，
  包括嵌套对象 / 数组、类型、枚举、范围、额外字段和声明的格式；保留原有输出字节上限。
  执行时不获取远程 Schema，引用必须可在提交的 Schema 内解析。

Task 的启动与工具回调通过同一个 `authorizeTaskRun` 调用宿主，检查当前主体、工作区及 Task key / version。
每次回调再检查当前允许的工具列表，通过后才预留预算；取消请求存在时拒绝新调用。
执行请求、账本及审批草稿使用本次授权结果；Task 宿主仍检查原工具凭证、具体记录与字段权限。
Conversation 继续使用独立 `ConversationToolHost`，实际授权范围和确认绑定沿用其原有规则，
不需要 ProcessID、NodeInstanceID 或 TaskDefinition。两类状态机、等待确认与恢复语义没有合并。

Module 工具接口与 SaaS 仓储 RPC 均保留预算错误 `agent.task.tool_call_limit` / `agent.task.cost_budget_exceeded`，
HTTP 状态为 429。执行预算不能靠重试原调用补充，因此错误的 `Retryable` 为 false。
新的仓储错误响应携带 code、class 和 retryable；Remote 验证 class 与 HTTP 状态一致后恢复 SDK 错误，
旧响应继续走兼容路径。未知数据库错误使用通用错误码，数据库及宿主的原始错误文字不进入仓储响应。
