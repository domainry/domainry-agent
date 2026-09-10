# Conversation 工具执行阶段记录

本记录对应 2026-09-10 的第一批开发进展，**不代表能力 TODO 全部完成**。
验证使用当前本地 `go.work` 中的 Agent SDK、Agent、Connectors 和 Identity；关联依赖发布、
脱离工作区的构建和真实外部模型工具联调仍未完成。

最新补充：个人待办、个人记忆、旧运行详情，以及知识资料撤权 / 恢复已完成一轮实际浏览器操作；历史回复与摘要的来源检查通过全量回归。准确范围见 [来源权限与网页验收](testing-2026-09-10-sources-and-browser.md)。下文锁屏及 Chrome 拦截记录属于此前尝试。

## 已实现的链路

- 现有 `ConversationModel` 保持兼容，新增可选 `ConversationAgentModel`。
  Chat Completions、Messages、Responses 实现工具定义、参数流、正常结束判定和
  工具结果回传。Messages 签名续接块、Responses 加密 reasoning 保存在内部步骤。
  纯文本摘要仍使用无工具接口。
- 顺序执行器验证 JSON Schema（2020-12，禁止外部 schema 加载），冻结逐步骤输入，
  验证模型配置指纹，保存结果后继续模型。限制步数、总调用次数、每步参数 / 文本、
  整体超时和序列化上下文；相同逻辑参数的重复调用计入循环限制。
- Migration 3 增加步骤与调用表，保留 Migration 1、2。所有变更依赖当前租约和
  fence。模型输入、工具版本、稳定幂等键、授权记录、结果和事件持久化；结果不明
  的写操作由宿主核查。已完成调用恢复时读取已存结果，仍重新授权。
- 五个只读工具：`time_now`、`calculate`、`history_search`、`history_read`、
  `memory_search`。计算使用 `big.Rat` 与明确舍入；日期区分日历天与实际秒数。
  历史搜索返回原始消息引用并分页，原文按 UTF-8 字节切片；记忆分页游标绑定当前
  用户、查询和记忆快照。
- Identity 注册完整工具动作 / 权限，执行时实时解析主体并评估 owner 数据范围。
  测试通过真实 Identity 角色权限发布接口授予和撤销权限；生产角色不会自动提权。
- Run 保存公开步骤快照，和步骤 / 工具 SSE 事件一起提交。网页展示调用准备、执行、
  完成、失败、已受理、结果不明及有界结果预览；参数未结束时只是预览。
- Migration 4 增加交互记录。`ask_user` 单独占一步，保存问题与选择，等待时释放
  worker；用户答复保存为原始消息，配对工具结果并以 user 消息进入下一模型步骤。
  只有挂载了交互持久化和答复授权的宿主才开放提问。
- `respond` 入口具有独立 Identity 权限，确认绑定用户、工具版本、参数哈希和
  定义哈希。确认与结果核查分别留账；答复幂等、拒绝、取消、24 小时默认过期、
  恢复后的实时授权均已接通。普通 resume 不能绕过补充或确认卡片。

## 已验证场景与代码依据

| 场景 | 证据 |
| --- | --- |
| 三种协议先返回工具调用，再接收结果并回答；截断、错误结束原因、回传不一致被拒绝 | [协议测试](../internal/infrastructure/provider/conversation_execution_test.go) |
| 完成写操作后第二轮模型失败，服务重建 / 恢复后写操作只执行一次 | [恢复测试](../integration/conversation_execution_integration_test.go) |
| 写操作返回结果丢失，恢复先核查；恢复时或模型下一轮前撤销权限，数据和操作均不继续 | 同上，以及 [持久化测试](../internal/infrastructure/persistence/database/agent/conversation_execution_store_test.go) |
| 注入模型忽略回调错误、超出预算、篡改完整参数，也不会产生工具效果 | [流式边界测试](../integration/conversation_execution_stream_guard_test.go) |
| 十进制精度、大整数、百分比、舍入、闰日、跨夏令时时间差、拒绝任意脚本 | [计算测试](../internal/application/conversation_calculate_test.go) |
| 用户 profile 时区优先级、跨日、下周一与显式时区 | [时间测试](../internal/application/conversation_time_test.go) |
| 关键词 / 时间范围、分页原文引用、跨用户拒绝 | [历史测试](../integration/conversation_history_integration_test.go) |
| 32 条含转义字符的记忆分批读取完整，跨用户及过期快照游标拒绝 | [记忆工具测试](../integration/conversation_personal_tools_integration_test.go) |
| 登录 → 提交消息 → 模型 HTTP SSE → 实际计算 → 模型继续 → 消息和事件落库 → SSE 重放 → 撤销授权 | [Web 完整链路测试](../internal/assembly/web/conversation_tools_test.go) |
| 参数增量、UTF-8 偏移、快照恢复、旧 attempt 拒绝、保留已完成步骤 | [前端状态测试](../frontend/src/conversation-state.test.ts) |
| 问题 / 确认等待释放 worker，旧租约拒绝提交；重复答复不重复写消息；批准与核查分别保存；取消、过期与删除同事务落库 | [交互存储测试](../internal/infrastructure/persistence/database/agent/conversation_interaction_store_test.go) |
| 服务重启后回答原问题，八次并发确认仅产生一次效果，确认后权限撤销阻止写入，工具版本变化拒绝，SaaS 客户端答复往返 | [交互集成测试](../integration/conversation_interaction_integration_test.go) |
| 提问与其他调用混在同一步时，本步不执行；缺少答复授权的宿主不发布提问工具 | 同上，以及 [个人工具接入测试](../integration/conversation_personal_tools_integration_test.go) |
| 真实 Identity 分别授予工具与答复权限；问答从 HTTP 模型到原文、事件、SSE 完整往返 | [Web 问答测试](../internal/assembly/web/conversation_interaction_test.go) |
| 真实 Identity 授权写入夹具，确认后结果不明，核查原调用得到结果，重复确认没有第二次效果 | [Web 确认测试](../internal/assembly/web/conversation_confirmation_test.go) |

浏览器实际验收使用临时 `127.0.0.1:8092`、真实 Identity Module、临时 SQLite 与
本地模型协议夹具，完成了登录、浏览既有调用、网页发送“再计算一次 0.1 + 0.2 元”、
展示“计算 / 已完成”和 `0.30`、刷新后恢复调用记录。测试服务已正常结束；没有
重启或修改 `8091` 上已有部署。浏览器检查还修复了残留的“当前不执行工具”文案，
计算卡片展示公式、金额、精度与舍入规则。

后续浏览器验收仍使用独立 `8092` 测试服务，已完成：问题出现后刷新恢复；
两个标签页查看同一问题，一页提交自由文本，另一页自动收到答复及完成状态；
确认卡片刷新后参数一致；确认后进入核查卡片，查询实际结果后完成；另一次请求
选择拒绝，刷新后仍为已停止，且不提供绕过拒绝的继续入口。确认夹具只写临时
内存账本，未连接真实业务写入服务。页面实际检查修正了等待时仍显示“正在生成”
和拒绝后仍提示“可重新生成”的文案。两次临时服务均正常结束。

## 复现

本轮结果：Agent 整仓测试、Agent SDK 整仓测试、下列三个包的 race 检查、
前端 10 项状态测试、TypeScript 检查和生产构建均通过。前述可选浏览器验收服务
在完成交互后正常退出，随后权限撤销检查通过。

```sh
go test ./...
go test -race ./integration ./internal/assembly/web ./internal/infrastructure/persistence/database/agent
npm --prefix frontend test
npm --prefix frontend run build
```

Agent SDK 在其仓库执行 `go test ./...`。需要浏览器人工验收时，在完成前端构建后运行：

```sh
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run TestPersonalTools -count=1 -v -timeout=20m
```

测试输出 ready 后访问 `http://127.0.0.1:8092`，仅使用测试文件里定义的临时账号
与口令。该服务的模型是确定性的计算夹具，不用于评价真实模型理解能力。
完成后 `POST /__acceptance/finish`，测试将继续检查权限撤销并关闭服务。该入口仅存在于
测试代码中，最长存活 15 分钟。不要将真实资料提交到此测试服务。

问答与确认的浏览器验收分别使用以下测试，不能同时占用同一端口：

```sh
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestConversationQuestionThroughIdentityHTTPProviderAndSSE$' -count=1 -v -timeout=20m
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestConversationConfirmationThroughIdentityHTTPAndReconciliation$' -count=1 -v -timeout=20m
```

前者会询问周报格式；后者提供只存在于测试代码中的 `create_fixture_record`，
确认后刻意返回结果不明，再通过核查读取原账本记录。两者都使用临时数据库和
真实 Identity 角色权限接口，答复与恢复经过正式产品 HTTP 路由。

## 个人记忆写入与本次请求授权

- SDK 新增 `memory_save` / `memory_forget`，版本修订用于检测并发修改；所有者从认证身份取得。
  新记忆 ID 根据持久调用幂等键生成；工具只能使用存储中冻结的定义与参数。
- `ConversationSend.write_scope.personal_memory` 由用户显式提交，随 Run 保存并计入消息幂等哈希，
  只授权本次请求管理自己的记忆。它不能增加 Identity 权限，也不会传递到下一条请求。
  未授权时沿用具体操作确认；模型提供的虚假确认无效。
- 本地记忆变更、调用账本结果和 `tool.completed` 事件在一个数据库事务中提交，复用原有表；
  写入再次校验当前 worker 的租约及 fence。重复请求读取回执，删除后的旧创建请求不会恢复数据。
- 网页增加本次记忆操作开关、记忆内容确认和结果显示。待确认的删除会读取当前记忆，显示内容及
  版本变化提示；接口仍以期望版本检查。刷新后不确定的发送保留原消息 ID 和原授权范围。
- Identity 的刷新任务以前只按内容哈希生成 ID，权限 A → B → A 会碰到已有记录。本次在
  `../domainry-identity` 修复为绑定发布版本和哈希，完成操作也绑定版本；旧任务完成不会影响后来的同内容发布。
  此修复已纳入本地 go.work 与 CI 源码依赖，发布版本仍待 H04。

验证证据：

| 场景 | 证据 |
| --- | --- |
| 事件写入失败时变更与账本一起回滚；保存回执恢复、旧创建不恢复已删数据、原始参数/幂等键/身份/fence 篡改被拒绝、取消后拒绝写入、严格修订号与幂等删除 | `conversation_personal_mutation_store_test.go` |
| 真实 Identity + 模型 HTTP 协议 + worker + SQLite + 产品 API：有范围直接保存、下一请求不继承范围、消息幂等键不能改变授权、等待时重启整个宿主、撤销单独写权限后拒绝确认、恢复权限后继续、重复确认、停用、拒绝删除及批准删除 | `internal/assembly/web/conversation_memory_test.go` |
| 允许的消息范围不能绕过撤销的 Identity 权限 | 同上 |
| 权限内容 A → B → A 重发、迟到的完成只关闭原发布 | Identity `definition_refresh_intent_integration_test.go` |
| 草稿和不确定发送保留原范围，完成后不继承到下一次发送 | `frontend/src/conversation-state.test.ts` |

已通过 Agent、Agent SDK、Identity 的完整 `go test ./...`，前端 11 个测试及 TypeScript / Vite 构建。
Agent 的存储、Web 和 integration 包，以及 Identity 的 metadata 存储与应用包已通过 race 检查。
其中原有重启测试在 race 下暴露了 20 ms 租约早于输入落库过期的问题；测试改为先完成快照写入，
再缩短租约并重开数据库，仍检查旧租约失效、fence 递增和冻结输入恢复，存储包重验通过。
模型为明确的本地协议夹具，记忆效果为真实产品存储；不代表真实外部模型已联调。
**本次新增记忆界面尚未完成浏览器验收**：CUA 返回 Mac 已锁定，已请求用户解锁；没有
为此启动临时浏览器服务，也没有修改 8091 部署。解锁后复现命令：

```sh
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestPersonalMemoryThroughIdentityHTTP$' -count=1 -v -timeout=20m
```

浏览器应覆盖：勾选本次记忆授权后发送“记住周报格式”直接完成；下一条不勾选并发送“修改周报格式”，
刷新后保留具体内容确认；批准后在个人记忆查看结果；发送“忘记周报格式”，核对删除目标，分别验证拒绝及批准。

## 个人待办：存储、工具与产品接口

新增 SDK 待办契约和五个工具：`todo_create`、`todo_list`、`todo_get`、`todo_update`、`todo_delete`。
Migration 5 保存个人待办和网页操作回执，前端与模型工具复用同一组存储规则。
`write_scope.personal_todos` 和个人记忆范围独立，重试不能扩大范围；独立 `/agent/todos` 路由避免
与 `/agent/conversations/{conversationID}/messages` 等现有动态路由发生交叉匹配冲突。
公开能力单列为 `agent.personal_todos`，没有超过 Foundation 每个能力的操作数限制。

| 已验证场景 | 代码证据 |
| --- | --- |
| 批次全部校验后创建、日期与时区、严格修订号、完成和重新打开、稳定原始序号、游标身份 / 查询绑定、响应大小与完整性 | `conversation_todo_store_test.go` |
| 批次数据已插入但事件写入失败时全部回滚；恢复读取同一回执；完成事件只出现一次；旧创建重放不恢复已删除项 | `conversation_personal_mutation_store_test.go` 的 `TestPersonalTodoBatchEffectReceiptAndEventAreAtomic` |
| 模型 HTTP SSE → 创建三项 → 网页接口完成第一项 → 定位原始第二项 → time_now → 具体修改确认 → 宿主重启 → 撤权拒绝 → 恢复授权后完成 → 重复确认无第二次效果 | `internal/assembly/web/conversation_todo_test.go` 的 `TestPersonalTodosThroughIdentityHTTP` |
| 两个批次时 ask_user 等待；用户选择较早批次后只修改其中原始第二项；原始答复与 SSE 可重放且没有重复消息 | 同上 |
| 未配置模型仍可使用有权限的待办接口；注册工具不自动授权；两名真实用户间读取 / 修改 / 删除 / 来源引用隔离；删除来源会话后待办与创建回执保留 | 同文件 `TestPersonalTodosWithoutModelAndAcrossUsers` |
| SaaS 客户端 → 认证 RPC → 应用服务 → SQLite：完整创建 / 查询 / 分页 / 修改 / 删除往返，保留来源、说明、原始序号与幂等回执；跨用户 / 工作区拒绝，运行时不匹配拒绝 | `integration/conversation_todo_integration_test.go` |
| 记忆与待办范围分离，刷新和不确定发送保持原范围；待办重试保持原目标、参数和修订号，只接受对应待办路由；日期展示保留指定时区 | `frontend/src/conversation-state.test.ts` |

本轮 Agent 与 Agent SDK 完整 `go test ./...`、前端 14 个状态测试及 TypeScript / Vite 构建已通过。
Agent 的 Web、存储和 integration 包均通过 race 检查。
所有 HTTP 模型决策均为明确的本地协议夹具，用户身份、权限、数据库、worker 和工具效果使用产品代码；
不能据此声称实际外部模型已通过多步工具或指代理解验收。

浏览器工具再次返回 Mac 锁定，本轮未启动临时浏览器服务，8091 现有部署未改动。
解锁后在完成前端构建的基础上运行：

```sh
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestPersonalTodosThroughIdentityHTTP$' -count=1 -v -timeout=20m
```

临时 8092 页面需验证：新会话中勾选待办授权并发送“整理成待办”；查看三项及来源，完成第一项；
下一条不授予待办范围并发送“把第二项改到周五”，刷新后核对原始第二项及具体日期再确认；
增加另一批后验证追问选择，完成后在个人待办查看结果；修改、删除、过滤和分页也通过实际界面操作。
完成后调用原有 `POST /__acceptance/finish` 关闭临时服务。该可选入口尚未完成本次浏览器验证。

## 工具结果压缩与按引用读取

本轮增加 SDK 的 `ConversationResultReference`、`ConversationResultRead` 与可选结果存储接口，
复用已有调用账本。`tool_result_read` 为独立只读动作，仍由 Identity 显式授予权限；它与 `ask_user`
一样在执行器内处理，外部 Provider 不持有执行凭证。预览只改变下一步模型输入，不修改原账本。

| 已验证场景 | 代码证据 |
| --- | --- |
| 大量中文、引号、反斜杠和换行的预览仍为合法 JSON，计入转义后的实际字节数，保留完成 / 失败 / 已受理状态、资源和错误码 | `internal/application/conversation_execution_context_test.go` |
| 超过上下文预算的原结果压缩后继续；断开模型再重建服务，复用同一冻结输入；原调用参数、文本及续接块不变，已完成操作和压缩事件均不重复 | `integration/conversation_result_integration_test.go` |
| 读取遗漏的尾部证据，以及在 32 KiB 上下文中多次压缩后逐页重建完整 JSON；最新请求的分片可实际进入模型 | 同上 |
| 读取其他已完成会话的结果后撤销原权限，下一次模型调用拒绝；对读取结果的嵌套引用也检查原来源 | 同上 |
| 错误校验值、缺失引用、工具版本变化、权限撤销、UTF-8 字节中间偏移及越界读取均明确失败，不返回私有内容 | 同上 |
| 未配置读取能力时，大结果不被静默截断；已完成操作仍保存；历史摘要为实际工具目录和步骤包装预留预算 | 同上 |
| 跨用户 / 工作区 / Runtime 读取拒绝，取消后仍可读取已存结果，删除来源会话后引用失效 | `conversation_result_store_test.go` |
| Chat Completions / Messages / Responses 均保留原生续接数据；应用侧压缩信息和结果链接字段不额外传入 Provider 协议 | `internal/infrastructure/provider/conversation_execution_test.go` |
| 真实 Identity + HTTP + SQLite：创建大量个人记忆 → 模型请求 memory_search → 压缩 → 模型取得实际引用并请求结果尾部分片 → 模型继续 → SSE 重放 | `internal/assembly/web/conversation_result_test.go` |

本轮 Agent、Agent SDK 完整测试及前端 14 个状态测试、TypeScript / Vite 构建通过。
应用、Provider、存储、integration 与 Web 包完成 race 检查，新增分页压力场景另行通过 race 验证。
模型是本地函数或 HTTP 协议夹具；没有将这些测试算作实际外部模型的理解能力验收。
Mac 仍被浏览器工具报告为锁定，未启动临时浏览器服务或修改 8091 现有部署。

## 跨轮执行引用与旧运行查看

新增 `execution_read`，共可装配 15 个个人工具（9 个只读、`ask_user`、5 个写入）。
本次扩展原有消息、步骤、账本和事件的关联，不新增表或迁移；旧账本同样可以查询。

| 场景 | 证据 |
| --- | --- |
| 最终回复省略资源 ID；下一轮从自动 Run 定位信息查到实际结果 | `integration/conversation_execution_read_integration_test.go` |
| 多次摘要替换且摘要不含任何资源 / Run ID；服务重建后通过历史搜索 → 原文 → 执行目录 → 完整结果恢复原始费用证据 | 同上，`TestExecutionReferencesSurviveTurnsSummariesAndServiceRestart` |
| 同一步多个调用分页、三类所有者隔离、已撤权或工具版本变化时不披露元数据 | 同上，`TestExecutionReadPaginationIsolationAndRevocation` |
| 读取后撤权、冻结输入后重启并撤权、再次读取目录结果时追溯原来源权限 | 同上，原操作调用次数保持不变 |
| 游标期间运行变化；取消仍保留成功与结果不明两类记录；删除来源后失效 | `conversation_execution_read_store_test.go` |
| 真实 Identity / HTTP / SQLite：先计算，再跨轮查询实际账本并读取原结果；旧 Run 接口和 SSE 引用一致 | `internal/assembly/web/conversation_execution_read_test.go` |
| SSE 工具结果引用在客户端归并及刷新快照后保留 | `frontend/src/conversation-state.test.ts` |

模型决策使用本地函数 / HTTP 协议夹具；不代表外部模型的自主理解已经验收。
网页新增独立查看窗口；Mac 再次被浏览器工具报告为锁定，本次未进行真实浏览器操作。
本轮 Agent 与 SDK 全量测试、前端 14 项状态测试、TypeScript / Vite 构建通过；
应用、数据库存储、integration、Web 四个包的 race 检查通过。既有工具说明保持不变，
避免纯文字调整改变冻结定义摘要；新增工具拥有独立权限，旧冻结输入可以继续使用原工具目录。

## A08 公共执行规则与 Task 回调验收

公共包 `internal/execution` 已被 Conversation、Task 及 Interactive 使用。没有增加迁移、公共 SDK 字段或前端入口。
旧 Task 的每次回调新增宿主实时授权检查；其输出采用与会话工具相同的完整 JSON Schema 校验。

| 场景 | 证据 |
| --- | --- |
| 调用 / 成本边界、负值、整数溢出、失败预留不修改使用量 | `internal/execution/budget_test.go` |
| 12 个并发请求只允许成本预算内的两次预留；错误 fencing 被拒绝；重建仓储后预算仍保留 | `agent_tool_budget_test.go` |
| 撤权、工作区或版本错配、未知主体、取消请求及已取消 context 在预算预留 / 宿主执行前被拒绝 | `task_tool_service_test.go` |
| Task 输出中的嵌套类型、枚举、负值、额外字段、错误日期和空数组被拒绝，与 Conversation 校验一致；远程 Schema 服务未被访问 | `task_execution_validation_test.go` |
| Module / SaaS 的真实 HTTP 工具回调、持久化账本与实时授权：撤权不计费，恢复权限后可调用，超额和取消后停止 | `integration/task_tool_gateway_integration_test.go` |
| 仓储错误保留预算类别与不可重试标记，错误信息不暴露数据库 / 宿主细节 | 同上，以及 `server/repository_errors_test.go` |

验证中修复了两个原有部署差异：Module 对 `rate_limited` 返回 500；SaaS 将预算错误改成通用仓储失败。
同时取消了原仓储对所有 `rate_limited` 错误一律可重试的执行预算行为。新的仓储错误契约保留显式重试标记，
兼容旧端点响应；Conversation SaaS 的 429 映射也补齐。

Agent 全量 `go test ./...` 通过；公共执行、应用、数据库存储、integration、server 和 remote 完成 race 检查。
宿主的业务授权与效果由测试夹具提供，不能据此声称实际业务系统、外部模型和剩余浏览器场景已经验收。

## K02 / K04 知识工具与当前权限

本轮将现有 Connector search / fetch 接入 Conversation 自主工具循环，并保留纯文本固定检索路径。工具模式的知识结果进入调用账本，使用相同的输入冻结、结果压缩与完整结果引用；不再重复执行回复前的固定搜索。

新增 `ConversationKnowledgeSource` 和可选 `ConversationToolResultAuthorizer` SDK 契约。执行器在结果重放、下一模型步骤、已完成步骤恢复，以及原始 / 嵌套结果读取时复核来源。Web Identity 的本地文档权限键通过服务端映射为上游 permission_ids，注册与授予分离；模型传入额外权限或范围参数会被 Schema 拒绝。

| 场景 | 证据 |
| --- | --- |
| 首次模型请求前没有固定检索；模型请求搜索，再按实际 doc_id 读取正文；参数注入被拒绝且没有请求上游 | `integration/conversation_knowledge_tools_integration_test.go` |
| 全文读取后模型断开，服务重建；撤权阻止原数据重放，恢复权限后复用完全相同的冻结输入；Run 留存原结果引用 | 同上 |
| 主体 / 来源隔离；宿主撤权、上游文档 ACL 变化、服务故障导致复核拒绝；JSON 数值精度及来源字段保留 | `internal/infrastructure/provider/knowledge_execution_test.go` |
| 真实 Identity 注册不自动授权；只有工具权限时看不到私有文档；授予文档组后 search / fetch 获准；撤权后 `tool_result_read` 不把已保存的私有结果交给模型 | `internal/assembly/web/conversation_knowledge_test.go` |
| 拒绝缺少知识服务 origin 的配置，避免错误地选择另一个默认域名 | `internal/infrastructure/provider/knowledge_test.go` |

验证命令：

```sh
go test ./...
go test -race ./internal/application ./internal/infrastructure/provider ./integration ./internal/assembly/web
(cd ../domainry-agent-sdk && go test ./...)
npm --prefix frontend run build
```

上述检查通过。模型与知识服务是本地 HTTP 协议夹具，Identity、用户授权发布、Agent、Connector、SQLite 和产品 HTTP 路由使用实际代码。没有访问真实知识内容、上传用户文件、改变已有 8091 部署或发布新版本。

浏览器尝试使用临时 8092 验收服务和已构建前端，Chrome 显示 `ERR_BLOCKED_BY_CLIENT`，未进入登录页面。随后正常关闭临时服务并完成后端断言。测试进程 PASS 只证明后端链路，**不证明浏览器操作通过**。源码中的可选 `AGENT_TOOL_UI_ACCEPTANCE=1` 入口可在浏览器可用后重跑。

仍缺：真实 Verdent 响应与命中 K01、来源字段映射和展示 K03、附件 / 解析 K05–K07，以及历史回复与浏览器快照副本的访问 / 清理 H02。当前 Provider 用最新权限重查后比较完整规范化 JSON；数据、排序或动态元数据变化也可能拒绝旧运行，应重新生成。后续确认实际 ACL / 版本契约后改为逐文档检查，不能以同一权限列表推断原文档依然可见。

## 尚未完成

- 个人记忆 / 待办的主要网页流程已完成一轮实际浏览器验收；删除、拒绝的补充场景与真实模型串联流程仍待完成，各业务动作的具体授权范围继续按 C04 推进。
- 跨轮定位与历史结果读取已增加代码和夹具验证；网页已验证旧运行保存的结果及最终回复，实际模型的自主查找仍需验收。
  原始用户输入、调用参数或续接块本身无法容纳时仍明确失败，保留已完成结果。
- K / F / J / N / R / L / G 各批能力，包括文档细粒度重授权、业务 / 报表 / 外部账号、
  成果、后台工作、计划任务和提醒。
- 真实外部模型的多工具联调、实际数据库部署矩阵、发布依赖与完整业务场景验收。
- 旧 Run 查看窗口已通过一轮浏览器读取与知识撤权 / 恢复验证；窄屏布局与活动运行并行查看尚未验收。

开发继续按 [能力 TODO](agent-capabilities-todo.md) 推进，上述缺项没有勾选或隐藏。
