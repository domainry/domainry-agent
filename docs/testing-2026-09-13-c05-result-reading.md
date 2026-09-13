# C05：专业工具的独立结果读取（阶段交付）

C05 仍未完成，完整条目保持 **4／25**。本批接通 Tools 的独立结果读取端口，以及邮件、日历和网页三个读取工具族；报表／分析、写入回执和其他来源 owner、跨用户协作及明确资料共享继续待办。

## 实现

- [Tools SDK](../../domainry-tools-sdk/tools.go)增加 `ResultReadAuthorizer`；调用方先授权包含结果的交付，来源 owner 再用当前资料权限验证保存的原结果。接口只校验，不执行、重试、对账或获取一份新结果。
- [注册与路由](../../domainry-tools/internal/application/tool/registry.go)要求明确的 `AuthorizeResultRead` 回调。选择器核对当前注册定义、主体、输入及输出 Schema、结果状态和大小，并应用超时。组合宿主按注册工具键路由，不能借另一工具或旧 ResultAuthorizer 的允许返回获取阅读权限。未注册独立策略返回明确的 unsupported；原有执行授权及来源检查仍可作为普通访问路径，来源明确拒绝时不能降级。
- [账号读取适配](../../domainry-tools/internal/adapter/accounttools/adapter.go)复用当前 Integration 的账号数据读取、列表范围、主体、账号版本、连接、Provider 和操作契约检查。邮件／日历／网页读取注册独立策略，不再依赖 Agent 工具 Action。工具关闭、账号禁用／撤销、资料范围变化仍会阻止旧交付。写入适配器没有隐式获得新策略。
- [Agent 交付来源核对](../internal/application/conversation_delivery_tool_sources.go)只在已授权的内部交付读取用途下消费独立端口，继续检查当前 delivery_read、完整注册定义及工具可用性。Profile、工具组合与确认装配保留该端口；接收者的执行工具配置不作为阅读生产者成果的许可。私有附件、Knowledge、成果、业务与个人工具保留各自的来源遍历，不能作为不透明结果直接放行。
- 直接执行详情、原结果分页、普通历史和模型执行保持原访问要求。独立来源拒绝立即终止；不存在通过旧执行许可绕过读取拒绝的分支。来源用途的缓存和私有附件隔离延续前一阶段实现。

## 验证

[真实 HTTP 场景](../internal/assembly/web/conversation_delivery_mail_permissions_test.go)在临时 SQLite 和隔离的 OAuth／邮件服务上完成：接收 Agent 通过正常 worker 读取一封邮件，模型夹具收到真实正文和服务器回执后提交交付。随后同一会话撤销 `mail.read`，只保留协作 view／delivery_read，交付正文、来源回执和交付历史仍可读取；直接执行及原结果读取被拒绝。即使额外授予 execution_read，原结果接口仍不能绕过原工具权限。普通模型请求再次执行 mail_read 时在调用开始前失败。

关闭并重开原临时数据库、重新登录后权限分离仍然生效。关闭工具偏好、撤回 Integration 账号数据读取权、撤销原账号都隐藏交付及验证信息。整个场景只有 **1 次**邮件服务请求，交付读取与撤权检查没有重新取数。测试中的模型是受控协议夹具，不作为模型能力评测。

[Tools 测试](../../domainry-tools/internal/application/tool/result_read_test.go)覆盖独立读取不调用执行授权、不授予执行／重试／原结果回放、当前来源撤权、修改定义／原请求、未显式注册以及组合路由。[Agent 测试](../internal/application/conversation_delivery_tool_sources_test.go)覆盖装配端口保留、执行 Profile 不限制受权交付、直接读取不能使用交付用途、来源拒绝不降级以及实时 delivery_read 撤权。原私有附件场景继续验证显式阅读策略不能隐式共享私有附件。

初次 HTTP 运行在偏好设置步骤失败：撤回原工具权限后，该工具已不在可编辑设置目录，夹具读到了空设置；调整为撤权前通过真实设置接口验证关闭行为。没有修改产品设置权限来放行夹具。失败日志保留。

全量回归、race 与原协作页面 Chrome 回归的最终结果见本批[证据目录](evidence/2026-09-13-c05-result-reading/commands.md)。

## 剩余来源策略

- Report 的 `AuthorizeQueryResult` 仍通过 `ActionReportQueryExecute` 解析当前主体；Analysis 的结果校验仍复用执行阶段的状态解析。需由 Report 公共契约定义阅读动作，保留发布定义、原结果签名、数据／字段范围和源版本检查，并贯通业务宿主传输和 Tools 适配。
- 账号写入工具的结果检查仍通过写入 Action 和写入 access；需明确只读历史回执授权，继续核对原操作与账号范围，不能用重试／对账代替阅读。
- Knowledge／业务成果来源按各自公开 owner 能力推进，不复制私有存储，也不修改当前用户身份来越过所有者隔离。
