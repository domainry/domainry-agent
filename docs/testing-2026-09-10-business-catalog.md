# 业务目录契约与真实 Identity HTTP 验收

日期：2026-09-10。本记录覆盖目录和实际服务 HTTP 验收；后续 [业务网页验收](testing-2026-09-10-business-web.md) 已补齐真实模型、浏览器和完整模块重启，J01 / J02 已完成。业务写操作仍未完成。

## 本批实现

- SDK 目录支持对象、动作、流程三种列表；列表独立分页，每页上限 25，按真实键展开参数。动作和流程可按对象过滤，全局流程可以独立发现。
- Runtime 目录使用当前主体可见 Schema、动作精确权限和实际流程入口策略。创建权限与读取权限分离，`readable=false` 的对象不接受记录查询和读取；停用、定时或无权流程不作为可调用流程展示。
- Runtime 与 OpenAPI 复用 `invocation.PayloadJSONSchema`，包含嵌套对象、数组边界、必填项、枚举、默认值、日期和金额字符串。未发布输入契约与显式空契约分别表示；不会从字段血缘猜输入类型，也不向模型提供审批或用户身份参数。
- Agent 校验目录种类、详情键、分页和返回集合，保存目录来源；动作 / 流程撤权后，旧参数说明不可继续提供。
- 真实启动测试发现 Runtime 只按旧 Agent / Task 定义决定是否装配 Agent。SDK 增加可选 `ConversationFactory`，Module 显式启用会话或配置模型时，Runtime 可以在没有旧定义的情况下加载会话，随后按原两阶段机制绑定业务宿主。

## 验证内容

`conversation_business_catalog_test.go` 使用真实 Runtime 记录服务与 SQLite、SDK 主体策略夹具，验证目录分页、创建权限与读取权限分离、动作参数、全局与对象流程、停用 / 定时 / 无权过滤、未发布与空参数契约、来源复核以及撤权后隐藏。

`action_payload_schema_contract_test.go` 使用实际 `ActionNormalizePayload`，验证目录声明的日期格式、金额字符串、默认值、嵌套必填项、未知字段及数组上限与 Runtime 输入规则一致。OpenAPI 原有嵌套 Action 请求测试继续通过。

`conversation_workflow_catalog_integration_test.go` 使用真实 Agent 会话执行和持久化、模型 / 宿主夹具，验证全局流程发现与参数展开、非法选择器拒绝、错误混合返回不进入模型和公开账本、权限撤销后的历史隐藏。

`TestConversationBusinessThroughManagedIdentityAndRuntimeHTTP` 启动实际 Identity Module、Runtime、Agent Module、HTTP 路由和 SQLite；模型决策为协议夹具，Data Exchange 使用 Runtime 既有测试工厂。步骤为：

1. 登录、修改初始密码、通过 Identity 管理接口分配业务角色。
2. 会话调用 `business_catalog` 展开客户字段，再分页发现客户动作。
3. `query_records` 仅得到本人客户，返回字段只有 `name`；`get_record` 使用刚得到的真实 ID 读取详情。
4. 五个模型步骤和四次工具调用持久化，HTTP 历史回复包含实际客户名称。
5. 通过 Identity 账号与角色接口切换到缺少 `customer.read` 的角色；该角色保留三个业务工具权限，用户仍可访问会话，旧客户回复被隐藏。

这里使用 Runtime 发布的角色定义和 Identity 管理的用户角色分配。Identity 自己的角色定义编辑接口未承载该 Runtime 来源角色，本次不据此宣称角色定义编辑已验收。

## 命令与日志

- Agent：`go test ./...` 和 `go vet ./...`，日志 `/tmp/domainry-agent-business-catalog-final.log`、`/tmp/domainry-agent-business-catalog-final-vet.log`，通过。
- SDK：`go test ./...`，日志 `/tmp/domainry-agent-sdk-business-catalog-final.log`，通过。
- Runtime：agenthost、action、invocation、openapi 四个受影响包全量，日志 `/tmp/domainry-runtime-business-catalog-full.log`，通过。
- Runtime 业务源并发检查：`/tmp/domainry-runtime-business-catalog-race.log`，通过。
- Runtime 独立会话启动专项：`/tmp/domainry-runtime-business-conversation-startup.log`，通过。
- Runtime 实际 Identity / HTTP 链路：`/tmp/domainry-runtime-business-identity-http.log`，通过；补充工具权限保留断言的并发检查见 `/tmp/domainry-runtime-business-identity-http-race.log`。
- Runtime 受影响代码静态检查：`/tmp/domainry-runtime-business-catalog-final-vet.log`，通过。三个仓库 `git diff --check` 通过。

Runtime 命令使用 `GOWORK=/tmp/domainry-agent-runtime-integration.go.work`。既有发布锁版本 / 能力摘要差异仍未解决，不能称为无 workspace 的正式依赖验收。

## 下一步

- Agent 网页的业务宿主组合及真实模型、浏览器查询 / 游标翻页、刷新、角色 / 字段变化和宿主重启验收已在后续增量完成，见上方链接。
- 补关联记录查询、业务动作执行、流程启动与状态跟踪。
- 继续知识库私有 ACL、产品附件、后台任务和提醒等清单剩余能力。
