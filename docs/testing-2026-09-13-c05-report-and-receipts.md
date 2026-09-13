# C05：报表、分析与账号回执的独立成果读取

本阶段继续 C05，完整清单仍为 **4／25**。报表／分析已完成独立来源校验、Tools 与 Agent 交付装配；账号写入回执复用 Integration 已有的只读账本端口。跨用户参与方与执行主体、Knowledge／业务来源的其余策略、明确资料共享入口仍需完成，不能把本阶段记作 C05 全部验收。

## 报表和分析

[Report SDK](../../domainry-report-sdk/result_read.go)提供可选 `ResultReader`，权限动作是 `report.results.read`。读取旧结果时仍检查当前发布定义、报表受众、对象／字段权限、原规格、来源版本和完整结果签名。原查询执行及原结果回放接口继续要求 `report.query.execute`。

单独撤回执行权也会改变 Identity 全局授权版本。因此不能直接复用原有执行范围摘要作为阅读凭证，也不能从旧签名中删除授权版本。[Runtime 的来源范围端口](../../domainry-runtime/runtime/modulehost/report/report_result_read_scope.go)重新编译当前受权对象和字段，绑定真实行谓词、关系条件、组织及业务范围，包括普通 JSON 不展示的授权字段；摘要不依赖与实际资料范围无关的执行权限版本。Report 在查询前后核对该范围，并签发独立 `ReadProof`。主体仍绑定原工作区／用户，本阶段没有跨用户转授权。

目录也签发独立阅读凭证，绑定保存的请求、页内容和当前定义／可见数据集。验证已保存的续页不重新执行历史游标。表格文件分析复用其来源服务的当前授权及完整投影版本／校验和；缺失来源范围证明时不签发可独立阅读的结果。

[业务 RPC](../../domainry-agent-sdk/businessrpc/result_read.go)新增四个有界转发操作。Runtime 每次解析当前 Identity，再通过 Report 公共接口检查；没有传入任意 SQL、用户指定的 ReportSubject 或执行凭据。Tools 对当前注册定义、请求摘要、实际来源身份和完整返回封装继续逐项校验。

[Tools／Agent 的阅读可用性](../internal/assembly/web/tool_settings.go)保留工具偏好及当前来源连接，但不再要求读者能发现可执行报表目录。只有内部已授权的交付读取用途可走此路径；原执行详情、原工具结果接口和普通历史没有取得新许可。

## 兼容性

旧成果没有 `ReadProof` 时，Tools 明确返回独立读取不支持，由 Agent 继续要求完整的原执行授权和来源检查。旧目录比较只忽略后来新增的凭证字段，元数据仍须一致。非空凭证被修改或来源明确拒绝时，不退回兼容路径。没有绕过原签名或给旧数据补造新凭证。

RPC 的新契约摘要为：

`fd3dbd5fd33572296c29d12d13545baffe458a835c0528c72b71f62eee86391b`

变化是四个可选方法和 Report／Analysis 结果及目录中的 `ReadProof` 字段。Go 公共接口采用可选端口；严格 JSON 传输需要客户端、服务端及部署 pin 同步更新。旧指纹不能当作兼容绑定。本次仅更新源码与测试，未发布依赖或部署服务。

## 邮件和日历写入回执

[写入结果阅读策略](../../domainry-tools/internal/adapter/accounttools/write_result_read.go)使用当前 `integration.connection_accounts.read` 解析账号访问范围，随后调用 Integration 的 `ReadConnectionAccountWriteReceipt`。该既有端口只读 owner 账本，按原执行者、原请求 ID、规范化原操作内容、源账号及当前 OAuth 状态检查，不领取写入任务，不刷新凭据，不请求 Provider，也不发送或重试操作。

Agent 传递已保存的原幂等键，阅读请求不带 worker 租约或执行确认。Tools 对照实际持久回执的调用 ID、时间、完成语义、来源和完整展示内容；未找到、失败、不确定或内容不同均拒绝。邮件受理仍不代表实际送达。发送账号目录阅读另需当前 list 和 read 权限；专业工具 Action 及账号 write 权限不再是交付阅读的前提。

原调用、恢复与结果回放仍保留原执行授权和确认要求。工具关闭、账号资料权撤回、账号更换／撤销及源操作契约变化都能阻止旧交付读取。

## 验证与限制

- [Report owner 测试](../../domainry-report/internal/application/report/result_read_test.go)：执行权撤回后的阅读；修改原请求、结果、来源范围、字段权限、源版本和主体均拒绝，未增加报表／分析执行次数。
- [完整表格来源测试](../../domainry-report/internal/application/report/analysis_table_result_read_test.go)：撤回分析执行权后仍可按阅读权校验旧结果；原文件、完整投影单元格、字段和来源访问变化均失效，未重开分析数据流。
- [真实 Runtime RPC 场景](../../domainry-runtime/runtime/bootstrap/integrationtest/conversation_result_read_rpc_test.go)：Report／Runtime／Identity／SQLite 与 Tools 的真实链路，查询与目录分页、分析、执行和读取权限分离、全 owner 重启、行范围及字段／对象权限撤回；通过业务动作修改实际源记录后旧结果失效。
- [Agent 报表交付 HTTP 场景](../internal/assembly/web/conversation_delivery_report_permissions_test.go)：正常 worker 保存四份工具回执并交付；撤回工具 Action 和 owner 执行能力后，交付及交付历史仍可读，且不再请求执行目录；工具关闭、独立来源撤权、原执行入口隔离与重启。此层使用记录精确结果的 owner 夹具；真实 Report 源权限由上项验证。
- [账号回执测试](../../domainry-tools/module/account_write_result_read_test.go)：发送／回复邮件、创建／修改日程，撤回执行和确认权限后的原账本读取，以及操作身份／内容修改、来源撤权和未知结果拒绝。Provider 与数据库行为另由真实邮件 HTTP 场景及 Integration 原账本测试覆盖。
- [邮件协作 HTTP 场景](../internal/assembly/web/conversation_delivery_write_permissions_test.go)：真实 Identity、Agent、Tools、Integration、Connector 和 Provider 适配器，外部邮件服务及模型为隔离夹具。精确确认后发送一次并交付账号目录和受理回执，随后验证独立阅读、撤权和重启；不得增加邮件服务调用。

所有模拟邮件地址和账号只存在于隔离测试内。没有操作真实邮箱，没有把受控模型夹具当作能力评测。完整测试命令、失败修正记录和最终源码摘要见[本阶段证据](evidence/2026-09-13-c05-report-and-receipts/commands.md)。
