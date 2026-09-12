# F05 独立业务宿主接入边界

依据 2026-09-12 当前代码：Agent SDK 已有 Business Source／Relation／Action／Workflow／EvidenceSealer，Module 延迟装配从宿主取得这些能力；Runtime `application/agenthost/ConversationBusinessHost` 已逐次解析真实 Identity，并经 Record、Action、Workflow owner 执行和复核。Agent Web 已能装配公开 Source；原 Agent SaaS remote 仍是 Agent 服务客户端，业务服务由独立 `businessrpc` 提供。

F05 复用上述端口，补齐可选服务接入。Agent SDK 新的独立 `businessrpc` 子包只拥有既有端口的有界 HTTP 协议、客户端与服务适配器；不拥有记录、业务规则、确认账本或报表执行。Runtime 在 bootstrap 组合既有真实业务宿主并提供可选服务 handler；Module 和该服务共用同一装配函数。Agent／Work／PM 的宿主只消费公开 SDK 客户端／本地 Source，应用层不导入 Runtime 实现。

该业务服务是受信任的应用间委托边界。服务 token 只能在受管宿主配置中使用，不能来自浏览器或模型；固定 Runtime／Workspace／Identity Application／Identity issuer，兼容性与来源身份在启动握手核对，每个请求再次检查。Runtime 的来源 identity 也包含 issuer，旧证明不能跨身份域沿用。来源仍须从 Identity 重新解析当前主体和角色、检查工具／具体业务／记录与字段权限。独立部署必须使用同一受信任 Identity 身份域，不把两个本地库中同名 user ID 当成同一人。不得保存或回放浏览器登录 token 为后台工作续权。

产品宿主新增 `Options.IdentityBinding`，借用已经限定应用的公共 Identity Binding，生命周期由调用方持有。借用时消费原宿主发布的权限，不注册应用或重写 Agent／Integration／Tools／Knowledge 权限清单；这避免多个产品启动时相互接管或退休同一工作区的权限。产品自有权限应由受管部署装配明确发布，缺失权限继续拒绝。原本地 Identity 和 ExternalIdentity 模式保留，不能同时配置借用与 ExternalIdentity。远程业务 Source 装配必须使用匹配 issuer 与 scope 的借用 Identity；实际独立进程／产品验证见[跨服务验收](testing-2026-09-12-f05-business-service.md)。

实际 Identity 远程 SDK 不提供可选角色目录发布端口。受管部署应通过已有管理 API 设置角色、字段策略，并给独立 Identity 提供声明的对象／字段目录；业务记录、动作和报表仍归 Runtime。工具设置的 `tools:user_preferences` 权限源也由部署方明确发布。测试夹具承担这项部署工作，没有增加产品或 Runtime application 到 Identity 私有实现的依赖。

HTTP 仅传既有类型化查询、受管执行元数据与来源证据。请求／响应、超时和路径受限，不跟随重定向，不自动重试。写操作的幂等键、精确确认与核查逻辑继续由 Agent 执行账本及业务 owner 处理；连接丢失／无可靠回执保持结果未知，不能以新请求盲目重放。服务不能根据客户端传入的 SQL、权限事实或自选系统身份绕开 Record／Action／Workflow。

报表执行归独立 Report Module／SDK，见 Runtime `docs/modules/report.md`。F05 已在可选 `ReportReader` 中复用 Report SDK 的 Summary／QueryObjectSQL 请求与结果；这里只按已声明 report key 查询，不接收 SQL、ReportSubject 或浏览器 token。Runtime application 依赖窄端口，bootstrap 才把当前 Identity principal 转换为既有 ReportAuthority，Report owner 保留报表、源记录、行字段权限检查和执行。Module 在 Report 绑定完成后组合相同端口，独立客户端通过同一服务调用。没有改动 Report 模块缓存、复制定义仓库或新增查询引擎。

新增两个报表方法后的服务契约 SHA256 为 `6faee392381def2a5122579ea66ad8c94c4a0800e1d425696995d813c6420a10`；旧阶段未发布契约及清单保留历史。Report 仍以字符串返回数值结果，JSON 参数用 json.Number 解码以保留整数精度。F05 不实现尚未到序号的 N01–N03：当前没有 report_query 工具，也不把旧 Analysis 的规格验证算作执行结果。后续 N01 需要在 Report 公共用例上实现工具目录与来源规则。

Work／PM 的 1.4.0 默认 profile 选择现有七个业务工具，业务 Skill 要求依据实际目录、分页、精确确认与原回执执行；配置选择不会自行创建业务 Source 或绕过当前授权。未接入宿主能力时仍不可用。

实施顺序：SDK 服务边界与隔离 HTTP → Runtime 真实宿主服务与 Module 共用装配 → 独立产品配置／授权／来源 → 真实跨服务、网页、写入回执与撤权／重启验收。证据写回 F05，完成前不进入 F06。

N01 已在实际 Report owner 补齐可选受权目录、真实查询来源及历史结果复核，宿主新增三个相应传输方法；当前契约与职责见[报表查询边界](report-query-boundaries.md)，验收见[N01 owner／宿主增量](testing-2026-09-12-n01-report-owner.md)。上文 F05 契约与交付结论保留历史。
