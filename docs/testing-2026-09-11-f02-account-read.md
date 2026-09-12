# F02 当前账号受权读取增量（2026-09-11）

在 Google 和 Microsoft 日历 Provider 后，完成 Integration 当前账号读取端口及 Module／SaaS／浏览器 SDK 接入。**F02 仍未勾选，整体 50 / 81**；当前继续 Tools 日历适配和产品装配，之后进行 Agent 页面与会话整段 E2E。

## 实现边界

- Connector SDK 增加可选 `OAuthOperationScopeProvider`，按已登记 operation key 固定范围声明并复制内外两层数组。未知操作或缺少声明不授予账号读取能力；空替代项明确表示无需 OAuth 范围。SDK 版本为 `v0.1.0-dev.17`、主契约 `connector-contract-v16`，SHA 为 `664cb625e96fed3fcfd88c6b25e0087c63aa18e22dd2c960ab9d858b85941fee`；日历子契约保持不变。
- Google／Microsoft 自己声明四个日历操作的实际 OAuth 范围。Google 的 freebusy 范围不开放事件内容，Microsoft 的 `Calendars.ReadBasic` 不开放正文详情；Graph 的短名称和完整 URI 由 Microsoft Provider 声明，Integration 不猜厂商别名。固定账号 probe 的范围仍独立。
- Integration SDK 的 `ConnectionAccountReads` 与管理调用、固定探测分开。公开请求只有 request ID、operation、contract hash 和 payload；subject 由可信宿主按当前动作生成，模型／浏览器不能提供 workspace、user、access、scope、config 或 secrets。Module 使用当前身份策略；SaaS 在经过服务认证的边界传递宿主 subject。
- Integration 应用服务只依赖内部领域类型和账号授权／调用端口；SDK 适配层完成 DTO 转换。账号持久层检查个人／工作区归属、配置状态、实际 grant，以及 Provider 注册的 call/read/hash／范围声明。执行层在解析凭证和 I/O 前复核连接修订、Provider 和操作 hash／effect；返回前再次检查账号归属、状态、grant 和来源修订。
- 复用现有敏感 invocation 路径，不保存查询、正文或厂商 response reference。请求身份绑定 actor、账号修订、操作契约和输入；重复成功请求仅返回原执行证据，`payload_available=false`，不会重放正文或再次发起读取。新 request ID 重新读取。历史结果消费者必须重新核对当前 Identity／工具权限和账号 source。
- 两条用户 HTTP 动作分别为 `/integration/connection-accounts/{connectionKey}/read-access`、`/read`，均声明 read effect、独立函数／数据权限及 no-store。用户账号和 OAuth 授权动作独立归为 `integration.connection_accounts` 能力分类，保持每个分类的操作数量上限；管理配置及其验证范围仍由连接管理分类负责。
- 没有新表、迁移账本、连接池、日历后台作业、写日程或邮件代码。没有让 Agent／Tools 直接读取 Integration 表或调用厂商 HTTP。

范围依据：[Google calendarList.list](https://developers.google.com/workspace/calendar/api/v3/reference/calendarList/list)、[events.list](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)、[events.get](https://developers.google.com/workspace/calendar/api/v3/reference/events/get)、[freeBusy.query](https://developers.google.com/workspace/calendar/api/v3/reference/freebusy/query)、[Microsoft Graph permissions](https://learn.microsoft.com/en-us/graph/permissions-reference)。Provider 的范围声明只允许请求，日历 ACL 仍由上游执行。

## 验证与直接证据

[机器清单](evidence/2026-09-11-f02-account-read.json)记录本次源码和[日志目录](evidence/2026-09-11-f02-account-read/)的哈希。

- [Connector SDK 完整 race／外部模块编译／身份测试](evidence/2026-09-11-f02-account-read/sdk-scopes-race.log)、[两个 Provider 范围与原读取测试](evidence/2026-09-11-f02-account-read/provider-scopes.log)通过。验证 Registry 单次／批次登记、声明快照不可变、未知／缺失／显式空要求的区别。
- [最终架构与账号读取 race](evidence/2026-09-11-f02-account-read/architecture-owner-final.log)通过。真实 SQLite 覆盖外人个人账号、其他 workspace、零权限、权限超过当前策略、hash 错误、未知／写／异步操作、缺少或无效声明、无 grant／只有 busy grant、撤销凭证、执行前修订／effect／hash 改变、返回前 grant／归属／状态变化、不同 actor／输入的重放隔离及敏感证据。
- [最终 HTTP 四组合 race](evidence/2026-09-11-f02-account-read/http-read-architecture-final.log)通过，33.083 秒。Google／Microsoft × Module／SaaS，使用实际 Provider、Registry、宿主 Transport、真实 HTTP、磁盘 SQLite、OAuth PKCE／加密凭证／刷新、身份策略 bundle、两次宿主启动。四个日历操作、全天日期、共同空闲、正文、跨用户与伪造参数拒绝、重启后凭证／重放、撤销全部通过；另建实际协议授权会话，仅授予 freebusy／ReadBasic，验证可查空闲、正文在上游 I/O 前被拒绝。
- [Integration 最终全量](evidence/2026-09-11-f02-account-read/integration-full-final.log)、[SDK 全量](evidence/2026-09-11-f02-account-read/integration-sdk-final.log)、[Connectors 全量](evidence/2026-09-11-f02-account-read/connectors-full.log)、[边界／Catalog](evidence/2026-09-11-f02-account-read/boundary-catalog.log)、[Integration 最终 vet](evidence/2026-09-11-f02-account-read/integration-vet-final.log)和[SDK vet](evidence/2026-09-11-f02-account-read/integration-sdk-vet.log)通过。
- [浏览器 SDK](evidence/2026-09-11-f02-account-read/browser-sdk.log)构建与 50 条客户端路由测试通过。[Agent Web 兼容回归](evidence/2026-09-11-f02-account-read/agent-web-compatibility.log)通过，87.502 秒；该检查发生在最终内部 DTO 分层修正前，最终对外行为由上述 HTTP race 再验证。

## 失败和修正

- [首次装配](evidence/2026-09-11-f02-account-read/transport-initial.log)暴露连接管理分类超过 Foundation 的 20 操作上限，已按用户账号职责拆分分类。[分类修正后的回归](evidence/2026-09-11-f02-account-read/transport-categories.log)仅剩旧 49 路由断言，更新为实际 51 条 HTTP 路由（50 个浏览器客户端动作）后，最终全量通过。
- [首次 HTTP race](evidence/2026-09-11-f02-account-read/http-read-race-initial.log)及[定位日志](evidence/2026-09-11-f02-account-read/http-read-diagnostic.log)发现新测试 Transport 把 `MaxResponseBytes=0` 当作零字节；实际宿主将其解释为有界默认值。修正夹具后刷新及后续读取通过，没有改动生产刷新语义。
- [首次 Integration 全量](evidence/2026-09-11-f02-account-read/integration-full.log)的架构检查发现我新增的应用服务直接依赖 SDK，违反现有内部边界。已使用内部领域 DTO，由 SDK 适配层转换；没有放宽测试。最终架构、owner race、HTTP race 与全量全部通过。

上游和身份策略使用明确的测试夹具；这里的真实 HTTP／SQLite 不等于真实 Google／Microsoft 账号，也不等于 Agent 日历页面 E2E。当前顺序继续 Tools → 产品 → 页面／会话验收，不进入 F03。
