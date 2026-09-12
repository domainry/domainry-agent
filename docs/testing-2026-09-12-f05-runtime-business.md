# F05 Runtime 实际业务宿主与身份域验收

本阶段将公共 `businessrpc` 接到实际 Runtime owner。业务目录、记录与字段、Action、Workflow 和证据继续由现有 owner 执行；没有在 Agent 或服务 adapter 中重建查询、数据库或幂等账本。F05 仍未完成，独立产品配置及其完整网页验收继续在本项内推进。

## 装配与身份边界

Runtime `bootstrap/transport.NewConversationBusinessSource` 组合已有 Schema／Record／Action／Workflow／Identity 端口。现有 Module 的 `BindAgentApplicationHost` 与新公开 `bootstrap.ConversationBusinessSource` 使用同一函数和宿主证据密钥。`bootstrap.ConversationBusinessHandler` 返回可选的 SDK 服务 handler；调用方负责挂载、应用间 token、监听器与关闭，不自动扩展 Runtime 的浏览器路由，也不开另一套模块数据库。

接入独立产品时发现原 Scope 只有 Runtime／Workspace／Application，不能区分独立身份服务中的同名用户。公共 Scope 现增加必填 `identity_issuer`，握手和每次请求校验它；Runtime 从真实 Identity binding 的 descriptor 取值。业务来源升为包含该 issuer 的 `runtime-business-v2`，旧证明不会因另一个身份域使用相同用户 ID、工作区名、业务内容或 HMAC key 而被接受。没有 issuer 的旧本地直接构造端口保留兼容，HTTP 服务要求明确 issuer。

本阶段 `domainry-agent-business-host-v1` 契约 SHA256 为 `38b96dc758553d733ea6e93efcd357475759166dbbe62f433ff9d5843689c0d2`，取代上一未发布阶段的 `9037f423…`。后续 Report 宿主增量再次扩展契约，当前身份见[架构边界](business-host-boundaries.md)。字段变化先使原冻结哈希测试失败，核对为新增 issuer 后更新期望；历史阶段源码／日志清单不改写。旧保存来源因命名空间变化需要重新查询，这是有意的授权失效。

## 真实 owner 与原 Module 回归

[最终 race](evidence/2026-09-12-f05-runtime-business/runtime-owner-issuer-race.log)通过，包 150.897 秒：

- `TestConversationBusinessRPCRealOwnersAndRestart`：34.78 秒，33 次实际服务 HTTP。真实 Identity 登录／角色、Runtime、Record、Action、Workflow 及 SQLite；覆盖目录、游标、字段读取与关联查询、证据封印、当前字段／对象撤权及恢复。没有使用假的记录执行器。
- 业务 Action 已提交后丢弃 HTTP 回应，客户端得到 uncertain；同一幂等键核查取得实际回执，数据库只查到一条 `RPC Created`。更改已确认参数不能创建 `Unexpected`。精确确认元数据由可信测试宿主产生，此用例不冒充产品确认 UI。
- Workflow 返回已受理且非终态；完整关闭并重开 Runtime、Identity 与所有数据库后，查询证据、Action 原回执、单一记录和原 Workflow process 均保持。缺失主体及跨工作区拒绝。
- `TestConversationBusinessBrowserAndRestart`：114.23 秒，复验原 Module 通过真实 Identity／Agent HTTP 的三次查询、单条读取、完整重启及字段／read 撤权。这里的 browser 是既有 HTTP 会话测试客户端；本次没有启动 Chrome，也未调用真实模型。

[Runtime 应用 race](evidence/2026-09-12-f05-runtime-business/runtime-issuer-initial.log)2.051 秒通过，新增 `TestBusinessEvidenceCannotMoveBetweenIdentityIssuers`：同 issuer 重开可复核旧证据，其他 issuer 即使使用相同业务依赖和签名 key 也拒绝。SDK 全量 race 和 vet 见[最终 SDK race](evidence/2026-09-12-f05-runtime-business/sdk-issuer-final-race.log)、[SDK vet](evidence/2026-09-12-f05-runtime-business/sdk-issuer-vet.log)；包含缺失／不同 issuer 的握手与逐请求拒绝。

[最终宿主与架构检查](evidence/2026-09-12-f05-runtime-business/runtime-final-check.log)通过：agenthost 0.682 秒、transport 1.000 秒、boundary 1.734 秒；[最终 vet](evidence/2026-09-12-f05-runtime-business/runtime-final-vet.log)覆盖上述装配及集成测试。

## 失败、清单复核与限制

- [首次 owner 编译](evidence/2026-09-12-f05-runtime-business/runtime-owner-initial.log)发现夹具过滤值应为现有 `json.RawMessage`，修正后[实际 owner 测试](evidence/2026-09-12-f05-runtime-business/runtime-owner-second.log)通过；后续 race 另行记录。
- [首次架构全量](evidence/2026-09-12-f05-runtime-business/runtime-host-boundary.log)两项失败：生成幂等清单漏记已有 `RunAgentWorkflowWithKey`，工作区审阅清单漏记六个拒绝条件。逐处核对其实际返回路径后更新清单，未改变任何业务实现以通过测试。[审阅证据](evidence/2026-09-12-f05-runtime-business/boundary-review.json)列出每处代码和判断。
- 首次整理工作区清单误将既有重复匹配去重，[失败日志](evidence/2026-09-12-f05-runtime-business/runtime-boundary-final.log)保留；随后保留所有重复项，仅新增六条已审阅条件，[边界复验](evidence/2026-09-12-f05-runtime-business/runtime-boundary-second.log)通过。没有扩大或删减原审阅门禁。
- [issuer 契约初次检查](evidence/2026-09-12-f05-runtime-business/sdk-issuer-initial.log)仅冻结哈希期望失败，实际身份域拒绝测试已通过；更新契约身份后执行完整 SDK race／vet。

当前服务验证使用真实本地业务 owner 和隔离 HTTP，未声称已部署独立业务服务或完成独立产品确认／网页。这些后续工作继续归 F05，报表实际工具按 N01 的独立 Report owner 接入，不在本阶段伪造 report_query。源码与日志见[机器清单](evidence/2026-09-12-f05-runtime-business.json)。
