# 关联记录查询实现与端到端验收

日期：2026-09-10。J03 已实现并完成真实模型、实际服务与浏览器验收；J04 / J05 业务写操作和流程执行仍未完成。

## 实现

- SDK `conversation_business_relations.go`：可选关系查询宿主契约与 `query_related_records` 工具，新增独立 Identity 权限；不支持该扩展的宿主不发布此工具。
- SDK `conversation_business.go`：增加 `kind=relations`，必须指定来源对象，按实际关系键分页。对象详情最多先列 10 个关系，独立目录每页最多 25 个。
- Agent `conversation_business_tools.go`：检查参数与支持状态，验证来源对象 / ID / 关系、目标记录字段和分页响应。错误宿主返回的其他来源、未请求字段或缺失续页游标被拒绝，不进入模型或公开结果。
- Runtime `conversation_business_relations.go`：从当前用户可见 Schema 发现正向 / 反向关系，使用已有记录服务读取起点与目标；固定关系条件由宿主添加。每次一层、最多 25 条，后续层级消耗正常运行预算。游标绑定起点、关系、目标查询和当前用户，不能跨客户或关系复用。
- 来源复核覆盖起点、关系字段、目标行 / 字段及完整结果；空引用返回空记录页。隐藏、脱敏或带逐记录条件的关系字段不作为通用可查询关系发布。
- 网页使用已有工具结果与旧运行窗口，新增“查询关联记录”名称；脚本支持 `--relations` 场景。

没有新增数据库直连工具或递归展开参数，也没有为本能力建立第二份业务数据存储。关系详情和记录继续由 Runtime 负责，执行账本仍归 Agent。

## 专项与实际服务验证

Runtime 的真实 RecordApplicationService / ORM / SQLite 测试覆盖：双向关系、同一对象的多个关系字段、客户订单项目、按金额的游标查询、目标字段选择、隐藏所有者 / 工作区、不允许的起点、隐藏和脱敏关系字段、空引用、目标记录无权限、游标跨起点 / 关系 / 字段拒绝，以及历史来源撤权与恢复。

单独构造 13 个关系，确认对象详情只列 10 个，沿 `relations_next_cursor` 找到其余 3 个；缺少起点的关系目录请求被拒绝。

Agent 集成测试经过实际 SaaS HTTP / Remote SDK、会话执行器和 SQLite，验证关系发现、两页查询、非法递归参数、错误响应拦截、历史撤权 / 恢复，以及无扩展宿主不会公布工具。模型和业务宿主在这一专项中是夹具。

`TestConversationBusinessRelationsThroughIdentityWebAndRestart` 启动实际 Identity、Runtime、Agent、HTTP 与 SQLite。临时 Manifest 提供合成客户、订单和项目，角色通过实际 Identity 接口分配。协议夹具从工具结果提取真实关系键、记录 ID 和游标，执行客户 → 两页订单 → 项目 → 客户；完整模块关闭重开后检查历史，并切换真实角色验证关系字段和起点读取权限撤销。Data Exchange 使用既有测试工厂，没有用它替代业务记录读取。

## 真实模型与网页

Verdent `gpt-5.6-sol` / Responses 的首次运行 `crun_c4e485ba074be11e31d78232cb9e3a67` 共 7 个模型步骤、12 次工具调用：7 次目录、1 次客户查询、4 次关联查询。自动断言 Order Alpha / 100、Order Beta / 200、Delivery Project、Acme，排除另一所有者的 PRIVATE-ORDER。

在实际浏览器操作会话 `conv_c07971b0f8092d6a7a6702133e42e058`：

1. 登录，打开已保存的真实模型结果，核对两条订单、项目和回查客户。
2. 展开第二页“查询关联记录”，确认起点为实际 Acme ID、关系为工具公布的 `reverse:order:customer`、每页一条、金额升序及实际返回的游标；结果只有 Order Beta / 200，`has_next=false`。
3. 从聊天输入框重新发起同一关联任务，实际执行四次关联查询，得到两行订单表格及项目 / 回查客户；截图核对布局。
4. 撤销 `order.customer` 关系字段读取权限，刷新后两轮回复隐藏，旧运行窗口的工具结果和回复也隐藏。
5. 恢复角色，在旧运行点击“刷新记录”，原关联结果恢复。
6. 撤销起点 `customer.read`，同时保留关联工具及订单 / 项目读取权限；网页刷新后两轮回复隐藏。
7. 恢复角色，完整关闭并重开 Runtime / Identity 模块与数据库，再刷新网页；同一登录、会话、两轮订单 / 项目 / 客户结果恢复。
8. 关闭临时标签页，结束测试控制服务；进程 PASS / 退出码 0。

浏览器通过测试专用控制入口触发角色分配和模块重启，内部调用实际服务。控制入口只存在于本地测试，未加入产品路由。重启期间测试监听器保留，不把模块重开称作操作系统进程崩溃恢复。

## 命令和证据

```sh
python3 scripts/test-agent-business.py --relations
python3 scripts/test-agent-business.py --relations --live --browser --model gpt-5.6-sol --protocol responses
```

浏览器模式需已有 `frontend/dist`，真实模式从既有配置及进程环境读取模型，必要时在终端隐藏输入凭证。密钥不进入代码、报告或测试结果文件。服务退出方式沿用 [业务网页验收](testing-2026-09-10-business-web.md)。

- Agent 全量：`/tmp/domainry-agent-relations-full.log`，通过。
- SDK 全量：`/tmp/domainry-sdk-business-relations.log`，通过。
- Agent 关系 / SaaS 及原有业务回归：`/tmp/domainry-agent-business-relations-regression-local-sdk.log`，通过；关系专项 race：`/tmp/domainry-agent-relations-race.log`，通过。
- Runtime 真实 Identity / HTTP / 重启：`/tmp/domainry-runtime-relations-http-2.log`，通过。
- Runtime agenthost 包全量：`/tmp/domainry-runtime-relations-host-full.log`，通过。
- Runtime 业务读取、关系及 bootstrap 专项 race：`/tmp/domainry-runtime-relations-race.log`，通过，包含原有业务查询回归。
- 关系目录数量边界：`/tmp/domainry-runtime-relations-catalog-bound.log`，通过。
- Agent / Runtime 静态检查：`/tmp/domainry-agent-relations-vet.log`、`/tmp/domainry-runtime-relations-vet.log`，通过。
- 前端构建：`/tmp/domainry-agent-relations-frontend.log`，通过，保留既有大包体积提示。
- Agent 本地 go.work 补齐后的普通命令验证：`/tmp/domainry-agent-relations-local-workspace.log`，通过，使用当前目录直接运行浏览器宿主专项，无需临时 GOWORK 覆盖。
- 真实模型与网页：`/tmp/domainry-relations-web-live.log`，通过，361.95 秒（包括浏览器验收等待）。
- 首次自动运行 JSON：`/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-business-agent-jeh_820p/business-relations-run.json`。后续浏览器步骤按实际 UI 操作记录，不把这个 JSON 称作浏览器断言。

本轮还遇到本地 Identity 新增 `organizationunit` SDK 契约，而 Agent 原 go.work 仍使用已发布 SDK，导致首次装配测试编译失败。Agent go.work 已加入相邻 Identity SDK，测试先使用匹配源码的临时 workspace 通过；没有修改业务实现绕过编译问题。依赖发布和 Runtime 版本锁仍属于 H04，不因本地组合通过而勾选。
