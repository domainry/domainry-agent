# domainry-agent

Source-owned Agent Runner / AI Gateway implementation for Domainry.

Public Agent/Skill/Task/Context/Routing/Runner contracts are owned by
`domainry-agent-sdk`. This repository owns validation and execution of those
contracts; Runtime does not redeclare them.

Persistent personal conversations are a separate `conversation.v1` capability,
independent of one-shot Interactive routing. See [会话接口、存储和配置](docs/conversations.md)
for durable messages, explicit user memory, automatic compaction, cancellation,
restart recovery, persisted streaming drafts and Gateway / compatible model protocols.
The optional `conversation.execution.v1` port adds durable model/tool iterations.
The Identity web host wires permission-scoped time, calculation, history, memory,
personal todo and user-question tools; see [阶段实现和验收范围](docs/testing-2026-09-10-tools.md).
Optional [文档检索](docs/knowledge.md) exposes permission-scoped search/read
tools when a tool-capable host is configured. Text-only deployments retain
pre-reply retrieval, including the bcri configuration.
Personal and shared libraries have separate membership management. Optional
host bindings connect each library to a dedicated remote KB, exposing a library
catalog and scoped search/read with live membership and Identity checks. See
[资料库权限与当前边界](docs/knowledge-permissions.md). Explicit managed bindings
now expose document upload, status, download and deletion via Module/SaaS APIs,
with durable indexing jobs and document-level filtering. The library dialog now
supports file selection, indexing status, verified original downloads and deletion.
Browser recovery, Identity revocation, host restart, citations and mobile deletion
have passed with the actual host and protocol fixtures; see
[文档网页验收](docs/testing-2026-09-10-library-documents-web.md).
Explicit [附件另存](docs/testing-2026-09-10-attachment-library-copy.md) now creates an independent library copy after checking source and target permissions.
Explicitly indexed private attachments now support Connector search/read in their
originating conversation, with persisted citations opening authorized originals.
Retrieval never parses or reads original bytes. See the
[private attachment retrieval verification](docs/testing-2026-09-11-private-attachment-retrieval.md)
and the [current capability TODO](docs/agent-capabilities-todo.md) for remaining work.
Conversation-only SaaS deployments can start without an Interactive/Task provider.
They require an explicit Identity application binding for current execution
authorization; the service key alone does not authorize queued user work. See
[SaaS Identity configuration](docs/conversations.md#模型配置与联调).

后续能力与交付顺序见 [Agent 日常工作能力 TODO](docs/agent-capabilities-todo.md)。

- `module`: in-process Binding selected by project composition.
- `remote`: SaaS Binding selected by project composition.
- `server`: authenticated SaaS protocol endpoint with Descriptor handshake.
- `cmd/domainry-agent`: standalone SaaS process entrypoint.
- `cmd/domainry-agent-web`: browser host embedding Identity and Agent modules; see [Identity Module 接入与启动](docs/identity-module.md).
- 外部账号登录：见 [Agent 复用已有账号与个人 Workspace](docs/external-account-login.md)。
- `internal/application`: Agent-owned dialog and execution-state use cases.
- `internal/composition`: the shared Module/SaaS Agent application and HTTP Adapter assembly.
- `internal/infrastructure/provider`: provider HTTP protocol, result normalization and stable error classification.
- `internal/transport/http/module`: source-owned product HTTP Adapter shared by Module and SaaS bindings.
- `definition`: Agent-owned validation before provider translation.
- `internal/infrastructure/persistence/base`: engine-neutral database foundation.
- `internal/infrastructure/persistence/{mysql,postgres,sqlite}`: database engine profiles.
- `internal/infrastructure/persistence/database/agent`: Agent-owned definition, runtime-state, task-run, interactive-run, worker and lifecycle repositories.

This module owns Agent definitions and provider-execution state, including task
claim/lease/fencing persistence. In Module mode it borrows the host database and
registers source-owned migrations in the host's sole `_schema_migrations`
ledger. In SaaS mode it persists the same model in an isolated Agent database.
Runtime workflow transactions never write Agent state; the former remote task
mutation/outbox bridge has been removed.

Agent owns conversations, sessions, proposals, approval decisions, task and
interactive state machines, tool-call ledger and Agent workers. Runtime retains
Workflow state, current business authorization and the concrete Record/Action/
Workflow/Report effects exposed through narrow Host Ports.

When business execution is configured, Module/SaaS bindings expose `dialog.state`
and `execution.state` through the same Agent-owned HTTP Adapter. Conversation-only
SaaS bindings expose the separate conversations Adapter and advertise their available capabilities. All `/agent/*` handlers,
route metadata and OpenAPI operations come from Agent; Runtime mounts the Adapter and applies host middleware without
duplicating Agent handlers. Host authorization, audit and business effects are
requested internally through `InteractiveHost`, `TaskHost`, `ProposalHost`,
`AuditHost` and `AnalysisHost`.

A Runtime Workflow Agent node calls `TaskRunner.Start` and receives `accepted`.
The later Agent worker runs outside that call stack, commits Agent terminal state
first, and then sends an idempotent `CompleteWorkflowTask` callback to Runtime.
Runtime commits only Workflow state and never calls Agent while handling that
callback, so the two capability directions do not form synchronous reentry.

本地浏览器对话验收：见 [AI Elements 对话页面](frontend/README.md)，构建前端后运行 `go run ./cmd/domainry-agent-playground`，默认打开 http://127.0.0.1:8090 。

## Reusable modules and product SaaS hosts

Agent definitions now select role instructions, Skills and tool subsets. Todo, Knowledge (including files) and Tools implementations live in their sibling modules behind public `module` facades. PM and office Work each own their domain rules and independent service/frontend entrypoints. See [module split and architecture verification](docs/module-split-delivery.md) for current source boundaries, startup documentation and verification evidence.

### 工作账号服务（F01）

原 Agent 网页、Work 和 PM 的共享「外部账号」页面可连接独立 Integration SaaS。产品宿主配置 `INTEGRATION_SAAS_BASE_URL`、`INTEGRATION_SAAS_TOKEN`、`INTEGRATION_SAAS_CONTRACT_SHA256` 三项；完全未配置时显示未连接，缺项或契约不兼容则启动失败。服务令牌只在宿主使用。Integration 的运行参数、持久加密密钥和 Provider 装配见 [Integration README](../domainry-integration/README.md)。

管理员登录并完成初始密码修改后，在「外部账号」中显式点击「启用管理员账号管理」，再通过「应用配置」登记厂商 Client ID、写入 Client Secret、允许的 Scopes 和精确回调地址。产品回调路径是 `/oauth/callback`；各产品的 origin 必须与厂商注册的完整回调地址一致。已有应用的密钥留空表示保留，读取配置不会返回密钥。其他角色继续由 Identity 权限管理分配具体账号动作，产品启动不会重新授予已撤销的权限。

账号及 OAuth 会话、密文、刷新和撤销由 Integration 保存；产品只挂载 Integration 公开 SDK 声明的十个账号／OAuth 动作。静态回调页清除 URL 中的 code/state 后，通过当前 Identity 会话完成授权；浏览器只保存待核对会话 ID、身份范围和返回位置。丢失换码响应时，返回页面读取原回执，避免重复换码。用户主动撤销的是 Integration 连接；不宣称撤回已完成的外部操作或厂商处的全部授权。

「工具设置」由 Tools 自有 Module、HTTP Adapter 和浏览器 SDK 提供。Agent／Work／PM 的宿主在最终 Agent／Skill 选择完成后装配它，借用宿主唯一迁移账本；偏好按 runtime／workspace／user 隔离，采用修订号条件更新。管理员需显式启用设置权限；开启开关不能增加工具或账号权限。响应结果不明时先刷新核对，关闭状态跨重启保留，并作用于实际调用、确认、恢复和旧工具结果复核。

需要外部账号的工具由产品宿主通过 `webhost.Options.ToolAccountRequirements` 声明 Connector／Provider 和必要 scopes。宿主只消费 Integration SDK 的安全账号状态；Tools 不读取 Integration 表、凭证或 token。当前本地凭证配置和 OAuth 已授予范围可判断可用性；这不是远端在线探测或永久授权保证，实际调用仍由 Integration／Connector 重新检查并按厂商协议刷新。尚无范围证据的旧账号不能满足带 scope 的要求。详见 [F01 工具设置产品验收](docs/testing-2026-09-11-f01-tool-settings-product.md)。


连接测试另按 Provider 声明核对实际 OAuth 范围。例如仅有日历只读授权仍可满足对应业务范围，但不能据此调用账号资料测试接口；页面说明缺少的范围，服务端在发起测试前拒绝，不自动扩大授权。测试结果不决定其他工具的权限。详见 [F01 测试范围验收](docs/testing-2026-09-11-f01-probe-scopes.md)。

日历读取由产品宿主的 `webhost.Options.CalendarTools` 选装，Agent 网页及默认 Work／PM 产品已启用。五个工具分别发现受权账号、列出日历、查询安排、读取详情和计算共同空闲；Tools 消费 Integration 当前账号读取 SDK，Agent 引擎不依赖厂商协议。管理员账号设置还会登记日历工具与账号读取权限，其他角色由 Identity 分配。Google 使用实际 Calendar 读取范围，Microsoft 使用 `Calendars.Read`（刷新另需 `offline_access`）；只有基础／忙闲范围时按具体操作限制可用性。未配置 Integration 不提供可用读取，外部账号服务配置及 OAuth 应用仍由 Integration 管理。

Work／PM 的默认 Agent 与日历 Skill 明确选择这五个工具，自定义配置仍需同时选择其 Skill 依赖。全天事件保留原日期及排他结束日期，时间窗口带偏移和 IANA 时区；分页与不完整忙闲结果不会被当作全部安排或空闲证据。关闭工具或账号窗口后，页面清理旧结果并重新核对访问权限。

邮件读取由 `webhost.Options.MailTools` 选装，Agent 网页及 Work／PM 默认产品启用四个工具：账号发现、邮件头列表、搜索和正文读取。日历与邮件通过中立的宿主装配组合，各自仅依赖公开 Tools 门面，执行引擎不导入邮件协议。基础 Gmail metadata／Graph Mail.ReadBasic 仅开放邮件头；内容读取范围才开放搜索与正文。OAuth 应用中提供的范围由服务管理员配置，用户实际选择并获准的范围决定工具可用性。

Work／PM 默认邮件 Skill 以实际正文生成摘要与待处理事项，缺失或截断须明确说明；请求保存的回复草稿进入 Knowledge 成果版本，标记“未发送”并保留原邮件引用及来源会话／运行。草稿使用已有成果界面预览、编辑和下载；每次访问复核来源工具、Identity 权限及账号状态。关闭来源工具或撤权时，原回复及派生成果无法继续读取、修改或导出。邮件发送／回复使用下述独立写工具，未提供厂商草稿写入。详见[邮件边界](docs/mail-read-boundaries.md)。

日程创建／修改和邮件发送／回复由 `webhost.Options.CalendarWriteTools`／`MailWriteTools` 独立选装，Agent Web、Work／PM 默认已选装；原读取开关仍只选择读取工具。七项工具包括两个写入账号发现入口、事件版本检查和四种写操作，通过公开 Tools／Integration SDK 使用当前账号、权限及实际 OAuth 范围。Identity owner 需授予各工具动作、账号写入与既有响应确认权限，开关和权限声明本身不授予执行权。页面显示准确账号／事件／系列范围、所有 To／CC／BCC、完整正文和明确清空内容，支持单项或冻结列表批准。写入后只按真实回执显示结果，邮件受理不等于送达；结果不明时核查原回执，不重发。选装写工具时默认参数预算 1 MiB、后续上下文 2 MiB，显式配置保留。实现、重启／撤权／60 KB 正文及网页证据见 [F06 产品验收](docs/testing-2026-09-12-f06-product-write.md)和[职责边界](docs/account-write-boundaries.md)。

公开网页读取由 `webhost.Options.WebTools` 选装，Agent 网页及 Work／PM 默认产品启用 `web_search`／`web_fetch`。产品宿主另配置 `INTEGRATION_WEB_CONNECTION_KEY`（Module 使用 `Options.WebConnectionKey`），固定选择 Integration 已登记的工作区服务连接。Integration 服务管理员负责 `INTEGRATION_WEB_PROXY_ORIGIN`、`web/llm_proxy` 的 `base_url`／精确 `allowed_source_hosts` 与私有 `api_token`，并通过公开管理及账号登记接口设置 workspace 归属。未配置 key 或未获当前工作区读取权限时不可用；模型不能选择其他连接或传入凭证，也不需要个人 OAuth。

Work／PM 默认 Web Skill 要求保留查询、原始／返回 URL、读取时间、裁剪和完整性提示，区分发布时间与读取时间；网页内容作为不可信数据。来源卡片与 Knowledge 报告沿用当前权限复核、版本编辑与导出；替换宿主服务连接也不能接管旧报告的来源授权。Agent 执行层与 Knowledge 无 Web 协议或 Provider 实现依赖，具体网络请求仍由 Integration／Connectors 处理。此处只接入 llm-proxy 已有的 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，分别供 `web_search`／`web_fetch` 使用；范围见[网页边界](docs/web-read-boundaries.md)。

独立业务宿主通过 Agent SDK 的 `businessrpc` 可选接入。产品入口读取 `AGENT_BUSINESS_ENDPOINT`、`AGENT_BUSINESS_SERVICE_TOKEN`、`AGENT_BUSINESS_SOURCE_IDENTITY`、`AGENT_BUSINESS_CONTRACT_SHA256`，并通过既有 Identity SDK 的 `IDENTITY_ENDPOINT`／`IDENTITY_ISSUER`／`IDENTITY_AUDIENCE`／`IDENTITY_SERVICE_ACCESS_TOKEN`／`IDENTITY_CAPABILITY_CONTRACT_SHA256` 连接同一身份服务。Runtime、工作区、应用及 issuer 必须完全匹配；Work／PM 可用 `SAAS_APPLICATION_KEY` 指定既有应用，Agent 使用 `AGENT_WEB_APPLICATION_KEY`。未配置业务服务时保留原本地部署；配置不完整或身份域不匹配时启动失败。

Report 宿主复用既有 Report SDK 两个按声明 key 查询的端口，当前 businessrpc 契约及验证见 [Report 宿主验收](docs/testing-2026-09-12-f05-report-host.md)。Work／PM 默认选择七个已有业务工具；实际 Source 和当前权限仍决定可用性，具体报表工具按 TODO 的 N01 接入。

程序装配可将公共 Source 放入 `Options.Agent.ConversationOptions.Business`，将现有 Identity Binding 放入 `Options.IdentityBinding`；Identity 的关闭及权限声明仍由原宿主持有。借用模式下产品不重写原权限快照，产品工具的权限声明须由受管部署发布，当前角色仍需获准。远程客户端只消费 SDK；实际业务服务由 Runtime 的 `bootstrap.ConversationBusinessHandler` 从现有 owner 装配。实现边界见[F05 边界](docs/business-host-boundaries.md)，实际共享 Identity 进程／业务服务和独立网页验证见[跨服务验收](docs/testing-2026-09-12-f05-business-service.md)。

共享 Identity 的部署方需要配置允许发布的权限 owner、产品工具设置权限源、业务角色和对象／字段策略目录。远程 SDK 未实现的可选角色目录发布能力不会自动生效；现有 Identity 管理 API 可供部署管理员配置。运行时仍逐次复核权限和历史来源，Identity 应用／HTTP 请求额度需按实际工作负载设置；本次隔离验收使用的额度与实测请求量见上述记录。

当前没有真实厂商应用或测试账号，OAuth／厂商响应以及日历、邮件模型使用隔离协议夹具。F01 单项实现与验证见[逐项核对](docs/testing-2026-09-11-f01-completion.md)；F02 实现及当前验收范围见[日历边界](docs/calendar-read-boundaries.md)。真实厂商授权、依赖发布和整体部署仍属于第三批及 H08 的验收范围。
