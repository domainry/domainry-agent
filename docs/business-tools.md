# 会话业务查询与动作接入

更新日期：2026-09-10。已实现 Agent SDK 契约、会话工具装配、执行记录及来源复核，并接上 Runtime 的业务宿主装配与真实记录读取服务。Runtime 的 SQLite / 行级策略 / 字段脱敏 / 游标翻页专项，以及真实 Identity / Runtime / Agent HTTP 的角色分配与撤权验证通过。现已接通 Module 宿主网页，真实模型三轮查询、字段 / 读取撤权、恢复和完整模块重启通过，见 [业务网页验收](testing-2026-09-10-business-web.md)。J01–J05 已完成，关联查询见 [关联验收](testing-2026-09-10-business-relations.md)，创建 / 更新 / 状态转换见 [业务动作验收](testing-2026-09-10-business-actions.md)；流程启动 / 查询见[流程验收](testing-2026-09-10-business-workflows.md)。F05 的外部连接、报表仍未完成。

## 当前代码

- SDK `conversation_business.go`：`business_catalog`、`query_records`、`get_record` 的固定定义，以及目录、字段、关系、动作 / 流程输入 Schema、过滤、排序、分页、记录和来源凭据的类型。
- `internal/application/conversation_business_tools.go`：将三个只读工具组合到现有会话宿主，保留个人工具、成果、提问及知识工具；检查固定定义、当前动作权限、参数、返回记录和分页范围。
- `ConversationOptions.Business`：宿主显式注入 `ConversationBusinessSource`。要求工具模型、实时动作授权和来源持久化；未配置时不装配业务工具，也不把声明的动作 / 流程当作可执行工具。
- `conversation_sources.go`：业务读取成为服务端来源依赖。恢复模型步骤、原始结果读取、历史记录、摘要和回复投影沿用现有来源审计；撤权或来源不可验证时不把旧业务内容重新提供给模型或用户。
- Identity Web 已注册三个独立工具动作，仍须显式授予权限；网页增加对应中文工具名称及稳定错误提示。注册不代表已连接业务系统。
- SDK `modulehost/conversation.go` 与 Agent `module/conversation_assembly.go`：可选的两阶段启动。Runtime 打开 Agent 存储时暂不启动会话；业务服务建立后，通过 `ConversationApplicationHost` 绑定当前授权与业务源，再构造会话服务和 HTTP 适配器。已排队运行在此之前不被领取；独立 Web 保持原启动方式。
- Runtime `runtime/application/agenthost/conversation_business_host.go`、`conversation_business_query.go`、`conversation_business_cursor.go`：按固定 Runtime / 工作区 / Identity 应用解析当前主体，投影可读取的对象 / 字段 / 关联，调用 `RecordApplicationService`；执行前拒绝隐藏字段、脱敏字段过滤和无效参数。目录已补对象、动作和流程的独立分页、按键展开及当前操作权限检查；只具备创建权限的对象可发现，但标记为不可读取。
- Runtime `runtime/bootstrap/runtime/agent_sdk_binding.go`、`startup.go` 和 `runtime/bootstrap/transport/http_integration_agent_handler_wiring.go`：已接入实际模块启动与应用宿主绑定。

## 宿主责任与输入边界

宿主实现 `agentsdk.ConversationBusinessSource`，可由 `module.Options.ConversationOptions.Business` 显式提供，或在两阶段模块启动时通过 `ConversationApplicationHost` 绑定；独立 SaaS 的会话服务可使用相同选项。凭证和真实服务地址留在宿主实现内。

`BusinessSourceIdentity()` 是稳定、非敏感的业务连接身份；更换业务来源必须改变身份。`BusinessCatalog` 只返回当前主体获准发现的对象与字段，按页列目录、按具体对象展开。`kind=actions` / `kind=workflows` 分页列出动作与流程，`object_key` 可过滤所属对象，`action_key` / `workflow_key` 展开实际输入契约；全局流程无需伪造对象关联。列表不展开输入 Schema，单次最多 25 条，避免把整个动作目录塞入上下文。默认对象详情只展开可读字段和关系，动作 / 流程按上述独立入口发现。

`QueryBusinessRecords` 和 `GetBusinessRecord` 必须在返回数据前检查实时主体、对象、行及字段权限，执行脱敏。过滤条件按 AND 组合，操作符及值类型必须符合当前目录。Agent 校验分页和返回记录 ID，拒绝指定字段以外的响应字段；这是对宿主协议的检查，不能替代业务服务的权限校验。

目录默认每页 10 个对象，上限 25；记录默认第 1 页、每页 20 条，上限 25。模型不能提交 runtime / workspace / principal、凭证、SQL 或任意连接参数。记录字段使用 `json.RawMessage`，保持金额及大整数的原始 JSON 精度。`total` 缺失表示宿主没有计算总数，一页结果不能当作全量数据。

Runtime 声明 `pagination: cursor`：后续请求沿用原过滤 / 字段 / 排序 / 每页数量，传回 `next_cursor`，可省略页码。游标保存当前排序位置、下一页号及来源 / 用户 / 查询摘要；它不是授权凭证，修改后也必须经过当前权限和类型校验。使用排序字段加 ID 定位下一批，空值统一排末尾，空字符串与 NULL 分别保存；SQL 继续通过 ORM 构造，保留 Runtime 原有深分页限制。首批返回授权范围内的总数，后续批次的总数留空，不把剩余记录数称作全量总数。数据在翻页之间可以变化，不能宣称这是固定时点的数据快照。

联调发现并修复 Runtime 原有写入与过滤转换将 `int64` 转为 `float64` 的问题。整数参数现保持 `int64`；实际 SQLite 写入、读取、筛选与翻页已验证 `9007199254740993`。已经被旧代码舍入的历史数据无法靠此修复还原。

成功调用保存 `ConversationBusinessEvidence`：固定来源、当前 runtime / workspace / user 的范围摘要、实际操作及原始参数、结构化结果。`RevalidateBusiness` 必须复核其中全部元数据和数据的当前访问权限；只检查工具动作权限不够。凭据中的 `input` 为实际冻结参数，宿主重新执行检查时仍需应用相同默认值。来源变化或无法验证时返回失败，不替换被冻结的模型输入。

Runtime 已增加可选的读取结果签名，用于衔接业务更新：宿主配置了持久的 `IntegrationSecretKey` 时，派生专用 HMAC 密钥，经完整重读核对后给结果附上 `host_proof`。签名绑定来源、所有者、请求、返回数据以及当时的有效权限和对象 Schema 摘要。密钥留在宿主，Agent 只保存不可作为授权凭证使用的证明。签发失败或证明超长时，整条结果不进入模型来源。

使用签名历史结果时仍解析当前 Identity、校验查询条件，并逐条复核当时返回的记录 ID。普通名称等字段更新不会改写或隐藏原回复；记录转移、删除、权限撤销仍会阻止读取。有效权限发生变化时退回完整重读比较，不能仅凭旧签名继续访问。条件字段仍遵循现有目录和记录策略；不能验证的历史值不放行。历史页数、总数与游标描述的是当时那次读取，不代表当前统计，也不承诺多个分页来自同一事务快照。

未配置签名密钥或旧结果没有证明时，保持原有完整重读比较。持久密钥相同的宿主重启可验证已保存的证明；更换密钥后旧证明失效，不做自动降级放行。业务目录继续完整重读比较。此改动支持动作执行后的历史结果复核；`invoke_action` 的接入见下方业务动作章节。

## 已验证

`integration/conversation_business_integration_test.go` 使用实际会话执行器、临时 SQLite 与宿主 / 模型夹具：

- 未允许的工作区参数在调用业务宿主前被拒绝。
- 目录发现、对象字段展开、分页查询与单条读取经过实际执行记录；分页信息和 `9007199254740993` 保持精确。
- 被拒绝的字段不提供给模型；宿主错误正文被转换为稳定错误码。
- 宿主意外多返回一个未请求字段时，整条结果在进入模型或公开执行记录前被拒绝。
- 模型中断后重建会话服务，权限撤销时拒绝继续，权限恢复后复用相同冻结输入。
- 完成的业务回复在来源撤权后隐藏；用户、工作区及 runtime 隔离通过。
- 公共 SaaS HTTP / SDK 路径通过实际目录 / 记录调用和历史撤权验证。

专项日志：`/tmp/domainry-agent-business-integration.log`。Agent 全量及静态检查、SDK 全量、前端构建分别记录在 `/tmp/domainry-agent-business-full.log`、`/tmp/domainry-agent-business-vet.log`、`/tmp/domainry-agent-sdk-business-full.log`、`/tmp/domainry-agent-business-frontend.log`。

## 本轮 Runtime 验证

- Agent `integration/conversation_module_binding_integration_test.go`：预先保存排队会话，验证绑定前不领取、缺少授权时拒绝绑定、绑定后恢复业务工具流程、重复绑定保留原服务、来源撤权后隐藏，以及无模型时仍提供个人 HTTP 服务。
- Agent `TestBusinessCursorPaginationContinuesWithoutInventedPageNumbers`：模型取得游标后，不填写页码也能继续第二页，结果及来源复核通过。
- Runtime `conversation_business_host_test.go`：真实 `RecordApplicationService`、字段策略、查询策略、ORM 与 SQLite；SDK 主体及权限数据为夹具。覆盖对象 / 字段目录、所有者 / 工作区隔离、脱敏、参数在读取前拒绝、大整数、升降序、同值、空字符串 / NULL、游标查询绑定和已保存结果的当前权限复核。
- Runtime `record_cursor_order_test.go`：SQLite / PostgreSQL / MySQL 的 ORM 排序 SQL 构造验证；实际数据库执行范围为 SQLite。

详细日志与未通过的发布锁检查见 [Runtime 业务查询验收记录](testing-2026-09-10-runtime-business.md)。

## 下一步

当前会话业务宿主直接复用 Runtime 的记录应用服务，不依赖旧 `AgentToolGateway` 的 Task credential / Interactive context。旧入口继续保留各自语义。

目录输入契约、字段权限变更、网页多步查询及真实模型端到端验收已通过。后续还需独立 Web 的外部业务服务连接，关联记录查询 J03 与动作执行 J04 已完成；流程启动 J05 已接入，验收进展见下文。本地 go.work 使用未发布依赖，Runtime 发布锁仍有版本及能力摘要差异，H04 保留未完成。

## Module 宿主网页入口

Agent 的公共 `web.NewHandler` 接受已有模块 bindings；Runtime 的 `bootstrap.ConversationWebHandler` 用它挂载真实会话路由和已有前端。浏览器边界验证当前 Identity 会话后，将可信令牌和工作区转发到 Runtime 的准入 / 授权 / 审计链路。调用方负责 HTTP 监听器，先停止监听器再关闭 Runtime；不启动第二套 Agent / Identity。普通独立 Web 命令仍保持原有装配，远程业务源的连接管理尚未由此实现。

`scripts/test-agent-business.py` 使用实际 Module 组合和合成业务数据，可选真实模型及浏览器验收；命令、具体操作和结果见 [业务网页验收](testing-2026-09-10-business-web.md)。

## 动作与流程目录增量

Runtime 与 OpenAPI 复用 `invocation.PayloadJSONSchema`。参数说明包括嵌套对象、重复项上限、必填项、枚举、默认值、日期及金额字符串。调用时仍执行 Runtime 原有规范化和校验；目录不会生成审批凭证、主体或其他宿主元数据。`input_schema: null` 表示列表未展开或动作没有发布输入契约；显式空契约返回禁止额外属性的空对象，二者不能混用。详情中的版本仅代表公开目录投影，不是执行授权或处理器版本。

流程使用实际调用边界的启用状态、手动入口规则及精确 `workflow.<key>.run` 权限；停用流程、定时触发流程和无权流程不作为可调用流程公布。挂载 Workflow 执行服务时，以它返回的当前定义统一生成名称、对象关联和执行契约；目标不可用时不回退到旧 Schema。历史目录也进入来源复核，动作或流程撤权后旧参数说明不可继续复用。目录发现不等于执行授权；动作执行 J04 和流程启动 J05 均已接入。

Runtime 原有启动判断只检查旧 Agent / Task 等清单定义。现增加 SDK `ConversationFactory` 启用声明：Module 显式配置 `ConversationEnabled`（环境变量 `AGENT_CONVERSATION_ENABLED=true`）或配置会话模型时，即使清单无旧定义也加载会话模块；仍沿用延迟宿主绑定。未配置会话且无旧定义时保持不加载。

本批代码、测试与限制见 [目录契约验收](testing-2026-09-10-business-catalog.md)。

## 关联记录查询

SDK 的可选 `ConversationBusinessRelationSource` 增加 `query_related_records`。未实现该接口的业务源只开放原有三个工具；注册工具权限本身不会让未接通的关系查询可执行。Runtime 已实现这个接口，查询通过既有 `RecordApplicationService.GetRecord` / `ListRecords`、字段策略、行策略和 ORM 执行。

`business_catalog` 支持 `kind=relations`，必须指定起点 `object_key`，按 `after` 分页，每页最多 25 个关系。对象详情先附带 10 个关系；存在 `relations_next_cursor` 时沿独立关系目录继续读取。每个关系包含目标对象、方向和宿主定义的键。正向关系读取当前记录所引用的对象，反向关系查询哪些记录通过指定字段引用当前记录；同一对象有多个关系字段时分别发布，模型不能自行拼接关系键。

工具输入为起点对象、实际记录 ID、已发布关系键、目标字段 / 过滤 / 排序和游标。每次只展开一层、最多 25 条；继续到下一层需要显式调用，并继续受会话步数与工具预算约束。目标对象和关系过滤由宿主解析，模型不能覆盖，也不接受递归深度或嵌套展开参数。

每页先检查起点记录读取权限，再检查关系字段及目标记录的行 / 字段权限。隐藏、脱敏或需要逐记录条件判断的关系字段不作为通用可查询关系发布。空引用返回空结果，不匹配任意记录。游标同时绑定用户、起点、关系定义、实际约束和目标查询；跨客户、跨关系或更换字段的游标会拒绝。

关联结果记录起点、关系、目标对象、实际记录和分页信息，进入现有持久执行账本与来源复核。历史回复、原结果读取和后续模型复用前重新检查整条来源；起点读取或关系字段撤权后，旧关联结果不可继续提供。内容或关系变更不会悄悄替换冻结的输入。

实际服务和网页验收使用 `python3 scripts/test-agent-business.py --relations`；增加 `--live --browser --model gpt-5.6-sol --protocol responses` 可运行真实模型和网页版本。该脚本创建临时合成客户、订单、项目及角色，不修改实际业务数据。

## 业务动作

实现可选 `ConversationBusinessActionSource` 的宿主可以提供 `invoke_action`。Agent 要求真实工具授权、持久交互存储及确认能力齐备；Runtime 通过 `WithConversationBusinessActions` 连接实际 Action 应用服务，沿用已有业务规则。没有挂载该能力的独立 Web 不会获得业务写入口。

模型先展开 `business_catalog kind=actions action_key=...`。可执行动作详情提供 `execution_version`、动作 `kind`、实际输入 Schema 及并发校验说明。执行版本绑定当前宿主与具体动作定义；普通目录投影 `version` 不能替代它。未公开输入契约的动作不会作为可执行动作提供。

`invoke_action` 参数只包含 `object_key`、`action_key`、`action_version`、可选 `record_id` 及业务 `data`。记录动作使用实际查询得到的 ID；启用乐观并发时，按目录提供 `expected_updated_at` 或对应版本字段。Runtime 校验实际主体、动作权限、目标类型、记录范围、当前动作版本和已声明字段，再调用 Action 应用服务。未经声明的审批元数据等字段不能通过业务参数注入。

当前每次业务写入都需要具体参数确认。网页显示目标、修改字段和版本约束；确认记录绑定用户、调用、工具定义与冻结参数。确认前刷新 / 重启后继续使用同一记录；确认后重新检查当前业务权限和版本。个人待办、记忆和成果授权不能覆盖业务写入；C04 的持久业务操作授权范围另行实现。Runtime 的额外业务审批要求继续生效。

Agent 提供稳定幂等键，Runtime 的持久动作回执记录实际效果。`completed` 仅表示宿主已完成该动作，返回执行 ID 和有界记录引用；具体业务字段仍须通过授权读取工具取得。重复确认不能制造新效果。遇到未知效果时，`ReconcileBusinessAction` 读取所属用户、工作区、动作与请求指纹一致的回执；已存在的处理中或可重试失败记录不被自动重新领取。只有确认没有该执行时，才通过不可重新领取已有执行的原子入口启动。

历史动作结果由 `RevalidateBusinessAction` 对照持久回执及当前访问权限复核，此过程只读，不再执行业务动作。权限撤销后隐藏相关回复，恢复后保留原来的历史内容。完整调用链由 Agent 会话账本及 Runtime 动作账本分别负责；没有新增业务数据库直写工具或第二套业务动作状态机。

接入过程中修复了两个实际边界问题：网页转发到 Runtime 时清除网页会话的简化身份上下文，由 Runtime 根据已验证令牌重新加载完整 Identity 权限；记录 mutation planner 使用 payload 副本，防止消耗乐观并发参数后使 Action 提交校验失败。网页收到宿主策略版本变化引起的 401 时刷新同一登录会话，保留原请求及幂等键，跨账号不重放。

`python3 scripts/test-agent-business.py --actions` 验证实际模块 HTTP；`--actions --browser` 验证网页；`--actions --live --model gpt-5.6-sol --protocol responses` 验证真实模型。实际后端、真实模型 3 阶段 / 22 次工具调用和网页均通过，具体结果见 [业务动作验收](testing-2026-09-10-business-actions.md)。

## 流程启动与进度

实现可选 `ConversationBusinessWorkflowSource` 的宿主提供 `workflow_start` / `workflow_get`。Runtime 通过 `WithConversationBusinessWorkflows` 挂载实际 Workflow 应用服务；宿主仍拥有流程定义、实例、审批任务和执行状态。独立 Web 未挂载该能力时不公布流程工具。

模型先展开 `business_catalog kind=workflows workflow_key=...`，取得当前 `execution_version` 与业务输入 Schema。`workflow_start` 只接受 `workflow_key`、`workflow_version` 和 `data`；身份、稳定幂等键及具体参数确认由 Agent 另行传入。每次启动都需用户确认，恢复后重新检查当前权限与执行版本。网页预览本次输入，刷新和宿主重启不产生新确认或新流程。

启动回执 `accepted` 表示已受理，包含执行 ID、流程实例 ID 和调用引用。Agent 不把受理当成审批完成；`workflow_get` 读取该实例当前状态、待处理步骤名称、业务结果及完成时间。`terminal` 也可能表示失败、拒绝或取消，必须结合 `status` / `business_outcome` 解释结果。流程查询沿用实际参与人权限；关联业务记录另行复核读取权限，不向模型提供原始流程变量或节点输出。

Runtime 复用既有执行回执及实例存储。会话稳定幂等键再按用户、工作区和流程限定；结果不明时只读核查所属回执。已存在的未完成启动即使租约过期也不重新领取；确认不存在时才经原子保护入口启动。旧 Workflow 入口原有重试规则保持原样。

历史进度保存当时的结果，服务端完整性证明绑定内容与当时权限；每次读取仍检查当前参与人、业务记录和来源权限。实际流程从等待变为完成后，历史“等待审批”记录保留原文；新的查询返回新状态。撤权后隐藏，恢复后复核，不通过重启流程来验证旧结果。

复现命令：`python3 scripts/test-agent-business.py --workflows`；真实模型与网页增加 `--live --browser --model gpt-5.6-sol --protocol responses`。脚本使用合成业务数据和临时数据库。实际 HTTP、真实模型、网页整段验收及相关 race 检查均通过；范围与证据见[流程验收记录](testing-2026-09-10-business-workflows.md)。
