# 剩余能力的拆库边界（2026-09-11）

本表依据当前拆库后的源码，约束后续按 TODO 顺序开发。只核对归属，不提前实施后续条目。清单已依次完成至 G06，当前 H01；旧验收日志是当时源码的证据，不能代替拆库后的组合验收。

## 当前已经存在的边界

- Agent 的 [应用层依赖检查](../internal/architecture/layout_test.go) 只允许公开 SDK／contract 和明确的确定性库；不允许其他服务实现、存储或装配层倒灌。
- Tools SDK 的 [中立工具契约](../../domainry-tools-sdk/tools.go) 已拥有 Authority／Definition／Request／Result／Host；Agent SDK 保留兼容命名。Tools [Registry](../../domainry-tools/internal/application/tool/registry.go) 负责注册、选装、逐次授权和输入／输出检查，[Combine](../../domainry-tools/internal/application/tool/combine.go) 负责明确扩展键的组合。Tools 独立持久设置模块已接 Agent／Work／PM 最终目录和执行链，并通过 SQLite 重开／CAS 与产品浏览器验证；部署白名单与用户偏好分别判断。
- Todo 的 [contract](../../domainry-todo/contract/todo.go) 拥有个人事项、批次、时间语义、版本与 CRUD；Todo 不是执行任务或调度任务。
- Knowledge 的 [公开装配入口](../../domainry-knowledge/module/module.go) 和 [记录契约](../../domainry-knowledge/contract/record.go) 拥有资料、成员、原件、索引、成果与结构化文档；Agent 的旧接口只是兼容入口。文件转换、索引与文档状态不能重新实现在 Agent 应用层。
- Work／PM 的 [产品装配](../../domainry-work/internal/assembly/saas/host.go) 选择产品模型、Agent／Skills 和工具。Agent 的 [通用宿主](../internal/assembly/product/product.go) 组合服务，业务规则仍在产品自己的 domain/application。
- Integration 的 [OAuth SDK](../../domainry-integration-sdk/oauth.go) 与 [账号 SDK](../../domainry-integration-sdk/connection_accounts.go) 拥有外部应用、授权会话、安全账号投影和撤销；厂商 HTTP 协议属于 Connectors，密钥及连接属于 Integration。Agent、Tools、Work／PM 均不保存 token 或查询 Integration 表。

## 按 TODO 顺序的实施归属

| TODO | 数据／规则所有者与接入边界 | 必须避免的回退 |
| --- | --- | --- |
| F01 | Integration：账号配置、OAuth、刷新、状态和撤销；Connectors：厂商协议。Tools：独立的工具设置契约、持久开关及可用性组合。Agent：消费中立可用性端口，在目录、执行、恢复和旧结果复核时应用。通用宿主：Identity、模块路由及静态回调页面；共享前端：消费 owner SDK 并展示页面。 | 不把 OAuth 回调、密钥表或 Provider 代码塞进 Agent／Tools；不把开关当权限；Tools 不直接调用 Integration 实现。宿主适配器通过公开端口组合账号状态和工具设置。 |
| F02 | Tools 的日历工具适配当前用户允许的 Integration 账号操作；Connectors 补缺少的 Calendar／Graph 协议。时间与全天事件语义在日历契约/适配器中；Agent 只处理工具结果。 | 不让模型传 workspace／user／密钥；不从 Agent 应用层直接访问 Google／Graph；不为日历查询穿透 Todo 表。 |
| F03 | 邮件读取／搜索由 Tools 工具适配 Integration／Connector；草稿作为明确的产品草稿或 Knowledge 成果，持有原邮件引用。 | 不把生成草稿视为发送；不把邮箱 token、全文无限制搬入模型或 Agent 的记忆存储。 |
| F04 | Connectors 仅适配 llm-proxy 已有的 `/tool/web_search`、`/tool/web_fetch_jina`；Integration 管连接与调用证据；Tools 发布 web_search／web_fetch 并保留来源。 | 不修改 llm-proxy 服务，不接入其模型或其他接口；Agent 不另做 URL 客户端，登录资源沿用对应账号连接。 |
| F05 | 复用 Agent SDK 已有 Business Source／Action／Workflow 端口。独立产品宿主通过 Runtime 公开接口适配；Module 借用已装配业务宿主。 | 不导入 Runtime 的 application／persistence；PM／Work 的自身领域规则继续由产品维护。 |
| F06 | 各业务 owner 执行写操作并持久化幂等／核查回执；Tools 适配版本化读写契约；Agent 保存精确操作授权、确认、执行账本与未知结果。 | 不由通用工具层重写业务规则；不因 HTTP 重试重放结果不明的发送或创建。 |
| N01 | Report／Report SDK 的可选 `GovernedQueries` 拥有受权目录、真实查询及旧结果复核。Tools SDK 定义声明，Tools 通过窄端口适配；Runtime 只在 bootstrap 组合 owner／Tools，application 转发 SDK 端口；Agent 启动前组合，Work／PM 默认选装。见[报表边界](report-query-boundaries.md)与[组合验收](testing-2026-09-12-n01-report-tools.md)。 | 不复活 Runtime／Agent 自建报表执行器，不读取 Report 定义仓库；校验成功不能当查询完成，最后续页不能当完整报表；具体执行与复核授权留在 owner，不重复读取全目录。 |
| N02 | Report 或数据 owner 负责受权完整数据集、聚合和来源版本；Tools 只做结构化输入及结果适配。已有 Agent Analysis 不能假装已实现完整执行。 | 不用几页检索结果冒充全量分析；不提供绕开业务权限的任意 SQL／脚本入口。 |
| N03 | Report／数据 owner 产出完整性说明与图表数据；Agent 保留工具结果与来源；Knowledge 拥有新成果版本。Report export 的任务／文件按已有边界归 Data Exchange。 | 不在 Agent 重复创建另一套 Report export 状态／下载接口；长执行交给 L 的任务能力。 |
| R03 | Knowledge 拥有成果正文、版本、下载和访问检查；Agent 拥有消息／运行链接；任务 owner 拥有后台任务链接。共享前端组合引用。 | 不搬回原件或成果存储；引用不会授予来源访问权限。 |
| L01 | Agent 的后台执行用例拥有目标、输入、授权、预算、执行账本和来源会话，工具仍使用 Tools SDK。业务效果继续归 Todo／Knowledge／Integration／产品 owner。 | 不把 Todo 当后台任务；不强迫独立 Web 填旧 Runtime ProcessID／TaskDefinition。 |
| L02 | Agent 的任务查询／事件回写使用稳定 ID 和当前权限；成果只保存 Knowledge 引用。 | 不复制业务 owner 的状态；“已受理”不等于“已完成”。 |
| L03 | Agent 管执行任务取消／恢复及递归上限；具体外部取消委托 Connector／业务 owner。 | 不盲目重放未知外部效果，不在同一 worker 同步等待自己。 |
| G01 | Scheduler 拥有计划定义、时间规则与运行状态；产品定义计划目标、用户范围与对话引用。参见现有 [Scheduler 边界](../../domainry-runtime/docs/modules/scheduler.md)。 | 不在 Agent／Work／Todo 另起一套 cron 或定时任务表。 |
| G02 | Scheduler 拥有时钟、领取、幂等触发、租约、补跑、重试和死信；宿主只装配 worker 或使用 SaaS。 | 不双启 Module／SaaS 调度者；不复用 Todo 截止时间作为调度状态。 |
| G03 | Scheduler 给出可信执行目标；产品宿主重新解析 Identity；Agent 接 L 的执行入口，Integration 检查账号状态。 | 不保存长期浏览器令牌；Scheduler 不写 Agent／Integration 表。 |
| G04 | Notification 拥有通知意图、站内 inbox、偏好、渠道投递与生命周期；Integration／Connectors 提供外部通道。参见 [Notification 边界](../../domainry-runtime/docs/modules/notification.md)。 | 不在 Agent 另建通知 worker／inbox；跨服务使用明确的幂等交接，不宣称跨库原子事务。 |
| G05 | Tools 增加计划管理适配器；Scheduler API 保存计划变更；产品页面展示当前计划。 | 自然语言解析不能绕过计划权限和版本检查；不改写 Scheduler 实现。 |
| G06 | Agent／产品拥有跟进范围、变化判定与执行结果；Scheduler 触发，Notification 按变化／完成／失败／需操作投递。 | 不把持续跟进实现为无期限占用 worker 的循环，不绕过停止请求。 |
| H01 | 每个 owner 提供自己的必要审计；Agent 汇总运行关联 ID／步骤／用量，产品组合查看。 | 不跨库直接拼 SQL；不记录 OAuth 导航 URL／code／token。 |
| H02 | 每个 owner 删除自己的数据与副本，跨 owner 清理通过公开请求、回执及恢复流程。 | Agent 删除会话不能直接删除 Knowledge／Integration 表；不得跳过来源权限。 |
| H03 | Agent／Tools 管执行预算与入口限额；Scheduler 管触发积压；Integration／Connector 宿主管外发限额与超时。 | 不用一个全局超时假装覆盖各个 owner 的配额；不依赖进程内计数完成多实例限流。 |
| H04 | 按依赖方向发布 SDK／各模块／产品，脱离 go.work 构建并验证外部消费；新拆出的 Tools／Todo／Knowledge／PM／Work 一并纳入。 | 不把本地工作区通过当作已发布版本可用；不把本地 replace 写入正式 go.mod。 |
| H06–H09 | 以最终实际产品和服务拓扑逐项验收，复用当前 owner 回执核实副作用、权限、恢复与完整性。 | 不拿旧拆库前日志、单包测试或协议夹具证明所有部署模式／真实外部账号。 |
| X01 | Connectors／Integration 管 MCP 连接与生命周期，Tools SDK 管工具声明与调用，Agent 继续用同一执行账本。 | 不让远端工具描述获得系统权限或自动扩大已选工具。 |
| X02 | 产品拥有具体 Agent／Skill 配置；Agent 定义层校验版本、输入输出、工具子集并执行。当前已有静态 Agent／Skills 配置，只补剩余可复用流程要求。 | 不把角色／业务步骤写死在通用 Agent，不允许 Skill 提升产品选装范围。 |
| X03 | Knowledge 拥有成果格式生成／转换、版本、文件与下载权限；可复用确定性格式库。 | 不在 Work／PM 或 Agent 重建成果存储，不用预览截图代替可下载的文档产物。 |
| X04 | 独立受限执行宿主／Connector 提供沙箱端口；Agent 只提交受权请求，Knowledge 接收获准产物。 | 不允许模型输入直接进入宿主 exec／数据库／任意网络。 |
| X05 | 独立桌面伴随端拥有明确本地授权、路径及应用访问；服务端消费其公开连接能力。 | 不从 Web 服务推断或访问用户电脑的任意路径，不把服务器文件权限当用户桌面授权。 |

实现时继续先读所属模块当前代码和契约，再补缺少的 owner 能力；相邻源码缺失不意味着允许在 Agent 内替代实现。每项的测试和证据仍落在原 TODO 下方，不改变顺序，也不提前勾选。

F01 当前工具设置已按上述边界接入三个产品：Tools 拥有持久开关和两条 HTTP 动作，Integration 拥有安全凭证状态及实际授予的 scopes，Agent 在最终白名单之后组合中立策略。源码、真实 Identity／浏览器／重启证据见[产品验收](testing-2026-09-11-f01-tool-settings-product.md)。厂商真实账号授权尚无配置，不把协议夹具当作真实账号通过。

F01 的固定账号测试范围继续留在 Provider：Connector SDK 声明可选范围契约并冻结注册快照，Integration 领域层按实际 grant 计算独立测试条件；Tools 的业务范围检查不依赖探测是否可用，Agent 页面只消费安全状态。[范围隔离验收](testing-2026-09-11-f01-probe-scopes.md)记录实际 Identity、浏览器、重启及依赖检查证据。
