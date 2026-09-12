# TODO 单项完成核对

> 2026-09-11 范围更正：本地原文件解析 / 持久正文缓存 / 私有附件模型提取已按用户要求移除；下文相关历史验收不代表当前能力。检索继续由 Connector 提供，原文件在线预览已完成 K06 验收。详见 [移除与升级说明](document-parsing.md)及 [K06 当前 TODO](agent-capabilities-todo.md)。

核对日期：2026-09-10。本记录将单项功能完成与整批交付验收分开。勾选表示该条要求已有实现及对应验证；未完成的外部服务接入、部署与整体验收继续保留在原条目中，不以单项通过代替整个 Agent 交付。

代码依据为当前工作目录；测试依据包括已完成的 Agent 全量测试 `/tmp/domainry-agent-knowledge-errors-full.log`、知识错误专项 `/tmp/domainry-agent-knowledge-business-errors.log`、真实模型日常工作 `/tmp/domainry-agent-daily-work-sol-responses-live-2.log`、待办浏览器补验 `/tmp/domainry-agent-todos-final-browser.log` 和前端构建。普通测试里的模型 / 外部目标为夹具；真实模型的范围见对应验收记录。

| 勾选项 | 实现与对应验证 |
| --- | --- |
| A01 | SDK `conversation_execution.go` 的可选模型 / 工具 / 流式契约；纯文本模型测试和工具协议测试均通过，原 Conversation 文本路径保留。 |
| A02 | `internal/infrastructure/provider/conversation_execution.go` 支持三种协议工具往返；`TestConversationExecutionProtocolsContinueToolsWithoutProviderSession`、流式截断与配对校验通过。摘要仍走无工具请求。三协议适配的夹具验证与 Responses 真实联调已通过；更多真实部署验收仍在 H08。 |
| A03 | 已补可选的实时连接／工具开关策略，目录、实际调用前和业务／知识历史复核均检查；保留元数据和冻结输入。实际 Identity／HTTP／SQLite 的 7 个网页场景与 8 个已提交 Run 通过，两次恢复复用原 Run，最终宿主 race 通过（141.25 秒）。代码、日志、失败修正和范围见 [A03 验收](testing-2026-09-10-tool-catalog.md)。F01 的账号授权／刷新／管理页面仍未完成。 |
| A04 | `conversation_execution.go` 顺序执行模型、校验、授权、工具及结果保存；Schema 校验、重新授权、续接集成测试通过，真实模型完成多步流程。 |
| A05 | 同一执行器落实步数、调用、耗时、参数 / 输出预算及重复参数调用限制；无效和截断流在工具效果前拒绝，相关执行与预算测试通过。 |
| A06 | `conversation_interaction.go` / `ask_user` 持久提问；问题重启续接、单独步骤约束、HTTP / SSE 和实际浏览器答复通过。 |
| A07 | `conversation_execution_context.go` 及 `tool_result_read`；完整结果分页重建、输入冻结重放、截断说明、原始与嵌套来源重授权测试通过。 |
| A08 | 已核对共用预算、完整 Schema 与实时授权规则，并新增独立会话宿主绑定入口，网页已改用该入口。绑定前不领取会话；重启恢复复核权限、保留原预算和已完成效果，旧 Task / SaaS 回调回归通过。全量、race 与实际 Identity / HTTP / SQLite 的 7 个网页场景、8 个已提交 Run 通过，旧 Task / TaskDefinition / Interactive 三表均为 0 行，见 [A08 验收](testing-2026-09-10-conversation-host.md)。 |
| B01–B03 | 执行表及逐步快照保存模型身份、调用 / 参数、结果与状态；冻结输入、模型变化拒绝、同键冲突和已完成写操作不重放测试通过。 |
| B04 | 未知写结果进入核查，按宿主声明的幂等能力处理；`TestConversationExecutionReconcilesUnknownWritesAndReauthorizesResume` 及真实 Identity / HTTP 核查夹具通过。实际业务 Connector 的故障验收仍在 H06。 |
| B05 | 等待用户 / 确认 / 核查状态、领取隔离、答复及过期持久化；`conversation_interaction_store_test.go` 和对应 HTTP / 浏览器验证通过。 |
| B06 | 取消保留已完成 / 已受理结果，未知在途写调用进入核查；原调用受限补存迟到回执，取消后禁止新步骤，恢复 / 删除 / 不同执行者的旧回执被拒绝。修复旧取消误停新 attempt 的竞态。实际官方 Connector HTTP 取消、4 个网页场景、刷新和完整宿主重启后的明确恢复通过；全量、race、静态与架构边界检查通过。详见 [B06 验收](testing-2026-09-10-cancellation.md)。 |
| B07 | 执行记录和公开事件使用同一事务，迁移由宿主按 Agent 所有权登记；执行存储测试及个人写入 / 成果效果与事件原子提交测试通过。 |
| C01、C02 | 已接入工具动作与交互权限的 Identity 注册、目录过滤和实际调用复核；个人资源在存储层按 Runtime / 工作区 / 用户限定，宿主负责提供真实授权事实。直接工具调用、跨用户和撤权测试通过；新增业务适配仍须在 J / F 按同一契约落实。 |
| C03（2026-09-11） | 独立 SDK 执行授权端口覆盖发送、恢复、确认后继续、后台领取、摘要／模型／工具和回复提交；网页、Runtime 与独立 SaaS 均接当前 Identity 主体。恢复／确认事务更新角色，冻结来源另行复核。真实 Identity 独立进程停用阻止排队及租约恢复的模型调用，启用后同一 Run 在 attempt 2 完成；4 个网页场景、全量、race 与静态检查通过，见 [C03 验收](testing-2026-09-11-execution-authorization.md)。 |
| C04（2026-09-11） | 已列出的具体操作支持单项或组合授权；同事务保存各项精确确认，重启恢复复用原调用账本，范围外新调用再次确认。8 个范围场景、SaaS、真实 Identity HTTP、实际 Runtime 两笔客户创建及 4 个网页场景通过，含撤权、缺参数、丢响应、重复提交和完整宿主重启；全量、race、静态及前端通过，见 [C04 验收](testing-2026-09-11-operation-scope.md)。 |
| C05 | 确认绑定实际调用、定义与参数摘要，重复答复复用结果；工具变化、撤权、重启和重复确认测试通过。 |
| C06 | 凭证停留在宿主 / Transport，公开状态不输出 Provider 续接数据；Provider 故障正文隐藏、公开 SSE 和知识业务错误泄漏回归通过。来源内容按不可信数据送入上下文；这不表示任意模型对提示注入具有绝对免疫。 |
| D01、D02 | `conversation_history_store.go` 和个人工具宿主；关键词、时间范围、分页、原始消息定位与所有者隔离测试通过。真实模型跨会话搜索与原文读取通过。 |
| D03 | 记忆查询、创建、更新、启停、删除及事务回执已接工具；Identity / HTTP / SQLite 重启与撤权验证通过，网页修改 / 停用及真实模型确认 / 拒绝 / 删除后刷新通过。 |
| D04、D05 | 待办五工具、独立表、批次原序号、修订与来源字段齐备；真实模型创建三项、改期及重启后完成原第二项通过。歧义批次澄清、网页删除后刷新且不重编号已验证。 |
| D06 | `time_now` 使用宿主时钟、实时用户时区并解析明确的相对日期；跨日、next_week、夏令时边界测试和真实改期通过。 |
| D07 | `conversation_calculate.go` 提供受限表达式、十进制计算、舍入、统计与日期间隔；大整数、金额、百分比、闰日及 DST 测试通过，真实 HTTP 工具往返验证通过。 |
| E01、E02 | AI Elements 工具状态展示及持久交互卡片已接页面；问题 / 确认刷新、跨标签页同步、拒绝与核查通过。 |
| E05（2026-09-11） | 按持久回执展示部分结果、已受理与待核查；具体步骤继续、确定失败的新修复草稿及原键核查通过实际 Identity / SQLite、Runtime 流程服务和 4 个网页场景验收，全量、race、静态与前端通过，见 [E05 验收](testing-2026-09-11-execution-outcome.md)。 |
| E03 | `TodoDialog.tsx` 的待办管理与聊天工具复用同一后端规则；新增、修改、完成、来源和删除刷新通过。390 像素手机导航、旧记录窗口及与当前等待流程并行查看已补验。 |
| E04 | 流式参数、步骤结果及多次模型输出进入持久事件并由前端归并；断流重放、无效流不执行、状态测试和真实 Responses 工具流程通过。 |
| K02 | `knowledge_search` / `knowledge_read` 自主调用已接同一 Connector，关闭工具模式固定预检索；模型不能提供 team / workspace / permission_ids 或任意文件路径。真实 HTTP 夹具及恢复 / 撤权测试通过。K01 / K03 / K04 的真实资料验收另行记录。 |
| K03 | 真实合成文档 search / fetch、4 条事实引用、SSE、重启、Identity 动作撤权 / 恢复、网页引用以及远端删除后的旧回复隐藏已通过，见 [真实知识库验收](testing-2026-09-10-live-knowledge.md)。上游只给 S3 地址时不伪造公开链接；HTTP(S) 链接显示另有夹具网页验证。 |
| R01、R02 | 六个成果工具中的创建、读取、编辑与版本存储已接入；Markdown、表格 / 图表创建、冲突、原段落编辑和重启验证通过，真实模型生成周报并修改第二节通过。 |
| R04 | Markdown / CSV 等已开放格式的指定版本导出、来源和下载权限、过期、审计及回执已实现；旧版本下载字节核对、SaaS / Identity / 存储测试通过。Office / PDF 仍是 X03。 |
| H05 | [真实日常工作验收](testing-2026-09-10-live-daily-work.md)：Responses 模型完成 8 阶段、25 次调用，覆盖历史、个人偏好、待办原项续办、完整宿主重启、成果编辑与下载。歧义纠正和记忆修改另有真实 Identity / HTTP / 浏览器夹具验证。 |
| J01、J02 | Runtime 目录与真实记录服务、SDK 和会话工具已接通；目录专项验证动作 / 流程输入契约与当前权限，记录专项验证类型化过滤、行 / 字段权限、脱敏、精度和游标。[真实模型与网页验收](testing-2026-09-10-business-web.md) 完成三轮查询、三页结果、详情、字段 / 读取撤权与恢复、完整模块重启和续查。Module 网页组合复用实际 Runtime 路由，独立外部服务适配仍在 F05。 |
| J03 | `ConversationBusinessRelationSource`、`query_related_records` 和 Runtime 关系目录 / 实际记录查询已接通。专项验证数量和层级限制、起点 / 字段 / 目标权限及游标绑定；真实 Identity、Verdent 模型与网页完成客户 → 两页订单 → 项目 → 客户、撤权 / 恢复和模块重启，见 [关联查询验收](testing-2026-09-10-business-relations.md)。 |
| J04 | `ConversationBusinessActionSource`、Agent `invoke_action`、Runtime 实际 Action 服务和具体操作确认卡片已接通。实际后端、真实 Verdent 模型和网页完成创建 / 更新 / 状态转换、重启、重复确认、版本冲突和撤权 / 恢复；3 阶段、22 次真实模型工具调用通过，见 [业务动作验收](testing-2026-09-10-business-actions.md)。 |
| J05 | `ConversationBusinessWorkflowSource`、Agent 流程确认 / 执行、Runtime 实际 Workflow 和网页结果已接通。实际 HTTP、真实 Verdent 模型与网页整段测试通过，604.86 秒；验证确认前刷新 / 重启、重复确认、受理与完成区分、实际审批、历史等待状态保留、撤权 / 恢复和拒绝不产生实例。最终实际数据库为两笔审批通过的流程；日志 `/tmp/domainry-runtime-workflow-live-2.log`，见[流程验收记录](testing-2026-09-10-business-workflows.md)。 |

继续保留未完成的关键项目：

- K05 最新增量（2026-09-11）：产品私有上传 / 索引 / 重启 / 引用 / 清理已通过真实单库验收（32.04 秒）。默认库权限范围、稳定请求 ID、持久权限摘要、私有 DELETE 回执、中文原名与 10 MiB 限制已接 SDK / 应用 / Connector / 网页；关闭上传后仍可读取。全量与相关 race 通过，见[本批验收](testing-2026-09-11-private-library-upload.md)。K05 随后已完成知识源目录 / 持久绑定、网页入口及真实三库产品上传（102.35 秒），见[完成验收](testing-2026-09-11-knowledge-datasources.md)。全部三份合成文件清理完成；绑定并发 / 撤权 / 重启、Module / SaaS、桌面 / 手机与全量 / race 通过。

- K01、K04：真实合成文档命中、读取、模型引用、网页及删除复核已完成；2026-09-11 新增权限矩阵验收程序、7 个反向场景及真实公开对照的 6 次 search / fetch，确认省略／空数组／随机错误 ID 仍可读取团队可见资料，测试文件已清理。已通过 Git 读取知识服务固定提交，补齐私有上传头与稳定请求 ID 契约；真实私有矩阵 7 个场景、26 次请求通过，正确权限可读、错误权限拒绝，旧来源在请求权限变化后不可复用，合成资料均已清理。结合既有真实空结果和引用验收，K01 已勾选，详见 [K01 权限对照](testing-2026-09-11-knowledge-permissions-probe.md)。K04 随后完成库级私有检索配置及真实三库验收：17 场景、95 请求，在途撤权模型调用计数 24 → 24，合成资料全部清理；代码、全量／race／脚本检查及机器证据见 [K04 验收](testing-2026-09-11-library-permissions.md)。
- K07 最新增量：[附件另存服务及网页](testing-2026-09-10-attachment-library-copy.md)已接通；用户明确选择个人／共享库，后端检查来源下载与目标上传权限，复制原件并启动持久索引，来源关系仅留在服务端。已验证源删除与提交竞争、SaaS 大文件、回执恢复、两侧撤权、完整宿主重启、跨会话检索及副本独立清理。原附件保持私有；真实多库和跨库移动仍未完成。
- K05 / K07 此前增量：第 10 个迁移、资料库原件存储、文档登记、持久索引 / 清理任务、Module / SaaS 五个文档接口及逐文档检索过滤已完成。当前 Identity 与成员撤权、删除隐藏、租约接管 / 迟到回执、索引状态、原件重启保留和 3.6 MB SaaS 往返通过；不确定推送不重传、核对后清理通过。Agent / SDK 全量、专项 race 和静态检查通过，见[应用链路验收](testing-2026-09-10-managed-documents.md)。新链路的知识服务 / 模型为夹具，资料库文档 UI 随后已接通，见[文档网页验收](testing-2026-09-10-library-documents-web.md)：浏览器续传与下载、撤权、完整宿主重启、对话引用及手机归档 / 删除通过。其后的真实多库产品上传与数据源入口已完成 K05，证据见本节最新记录；共享移动仍在 K07。三库检索授权已通过 K04。此前 Connector 的真实服务验收见[协议记录](testing-2026-09-10-knowledge-document-protocol.md)。
- K04–K07：个人 / 共享库实体、三种成员角色、七个公共管理接口、实时 Identity 检查及网页已接通，见[资料库管理验收](testing-2026-09-10-knowledge-libraries.md)。本批补独立远端绑定、可读库目录、按库 search / read、带库引用及历史权限复核；已验证双用户、成员与 Identity 撤权 / 恢复、冻结步骤恢复、重启和网页检索 / 引用 / 归档隐藏，见[按库检索验收](testing-2026-09-10-library-knowledge.md)，模型与知识内容为夹具。此前私有原文件、五个附件服务接口、网页选文件 / 状态 / 文本预览 / 删除、Identity 权限及持久清理队列已接通，见[附件网页验证](testing-2026-09-10-attachments-web.md)。浏览器附件下载落盘与上传故障恢复 UI 已通过[另存批次补验](testing-2026-09-10-attachment-library-copy.md)；资料库文档管理 UI 已完成；共享移动、字段提取、PDF / Office 解析预览仍未完成；生产私有上传单库链路已在上述 K05 最新增量通过；底层私有 ACL 协议已通过 K01，资料库私有检索与成员撤权已通过 K04。权限按个人资料＋共享资料库执行，不做组织树继承。
- R03：成果网页预览 / 编辑 / 下载已验证；后台任务关联尚未接通。
- F、N、L、G：实际外部账号、分析、后台任务和调度提醒尚未形成会话产品交付。J01–J05 的业务查询、关联、动作和流程已按专项、真实模型及网页证据勾选。
- H01–H04、H06–H09：运行审计、保留 / 配额、依赖发布及对应实际外部服务和部署验收仍有缺口。
- X：保持可选范围，未实施项不勾选。

### 2026-09-11 K06 增量

K06 按用户最新要求改为 Connector 检索结果到受权原文件的在线展示。本地原文件解析、正文缓存与私有附件提取已删除，数据库升级清理与旧来源拒绝回归通过；PDF / DOCX / XLSX 查看器的资料库、检索引用和私有附件网页验收完成。补验七项界面基线均通过，跨会话隔离、重启、撤权 / 恢复、精确下载及物理删除通过，未产生解析缓存。此前各解析增量及失败记录作为历史证据保留，不计为当前能力。详见 [当前边界](document-parsing.md)及[完成验收](testing-2026-09-11-original-preview.md)，K06 已勾选，总数 48 已完成 / 33 未完成；下一项 K07。
