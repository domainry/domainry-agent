# F01 个人／工作区连接归属与公开授权边界

2026-09-11。按 F01 接续顺序完成连接归属这一子步骤，后续是首次 OAuth，再做 Agent 账号页面与工具开关。**F01 仍未完成，保持 49 / 81；没有进入 F02。** 本记录包含当前工作区原有未归档归属代码的核对、修复和本次实际执行的验证。[机器证据](evidence/2026-09-11-f01-connection-ownership.json)保存源码、日志及工作区配置哈希。

## 实现与边界

- Integration SDK 新增独立 `ConnectionAccounts` 当前用户端口及 `ConnectionAccountAdministration` 管理端口。安全账号投影只含连接标识、所属工作区、Provider、展示名称、明确的个人／工作区归属、状态与时间，不暴露配置、密钥引用、指纹或创建者信息。
- Integration 持久保存独立归属，旧连接必须明确登记才出现在账号目录中。归属不能原地转让或改为共享；重复登记相同归属可恢复。`CreatedBy` 仅作创建证据，不能代替所有者授权。
- 宿主对每次具体账号操作编译 Identity 权限，再通过 `ConnectionAccountSubject.Access` 传递允许的个人／工作区范围；零值拒绝。`owner` 只允许当前用户的个人连接；`all` 仅在该具体账号 Action 上增加工作区连接，不允许读取别人的个人连接，也不授予配置或凭证管理权限。组织范围不映射为账号所有权，不可表达的组织拒绝按拒绝处理。
- Module 从已经认证的 Principal 与当前 Action scope 构造该投影，忽略浏览器伪造的身份／授权查询参数；SaaS SDK 将同一闭合投影传过受服务令牌保护的服务接口。存储再次检查本地 Context 有无更窄范围，不能在远端因为缺少内部 Context 而获得额外权限。
- 当前用户测试只接受固定连接检查，明确拒绝业务操作、Payload／Input 和写操作确认参数。核对发现原底层 `Management.TestConnection` 本来就只调用 `ConnectionTester`，本次收紧的是上层契约，**没有宣称复现了任意业务调用漏洞**。账号测试不返回 Provider 原始诊断正文；错误采用固定文本，调用后重新读取账号状态与版本。
- 撤销在一个宿主数据库事务中检查当前所有权及连接版本，再同步撤销连接与所登记的活动凭证。对已经撤销的连接重放原请求返回相同结果，可恢复响应丢失。调用中撤销后，迟到刷新不能恢复凭证。
- 已登记账号只允许独立的 Integration managed material；拒绝其他已登记或旧连接借用其凭证，也拒绝把已被旧连接共用的凭证登记为私有账号。失败登记／改配使用原事务回滚。
- 新表及索引由 ORM migration builder 生成，沿用宿主事务、迁移锁和唯一 `_schema_migrations`；本批归属迁移为版本 4。没有新建迁移账本。业务规则投影位于内部 domain model；持久化、HTTP 装配分别留在 Integration 所属层。Agent 没有导入 Integration 内部代码，也没有保存凭证副本。

代码：SDK [账号公共契约](../../domainry-integration-sdk/connection_accounts.go)与[远端适配](../../domainry-integration-sdk/remote/factory.go)；Integration [数据范围投影](../../domainry-integration/internal/domain/integration/model/integration_access.go)、[归属存储](../../domainry-integration/internal/infrastructure/persistence/database/integration/connection_account_store.go)、[Module HTTP](../../domainry-integration/internal/transport/http/module/management.go)、[SaaS HTTP](../../domainry-integration/internal/transport/http/saas/management.go)和[迁移定义](../../domainry-integration/internal/infrastructure/persistence/database/schema/definition.go)。

## 实际验证

| 层次 | 覆盖 | 证据 |
| --- | --- | --- |
| 持久层 8 项 | 个人／共享／旧连接分离、明确授权与缩小范围、错误工作区、不可转让、凭证借用拒绝与回滚、固定测试输入、安全返回、版本冲突、撤销重放及调用期间撤销 | [专项](evidence/2026-09-11-f01-connection-ownership/targeted-initial.log)、[最终 race（含第 8 项）](evidence/2026-09-11-f01-connection-ownership/race-http.log) |
| 公共 Module HTTP | 仅使用公开 module／SDK 组合真实 HTTP、磁盘 SQLite 和 AES-GCM；双用户／跨工作区、伪造查询参数、错误 Action、撤权恢复；刷新后业务失败仍保存新凭证；两次关闭并重开整个宿主后使用刷新值、撤销仍有效；原撤销请求重放和普通调用拒绝 | [初次通过](evidence/2026-09-11-f01-connection-ownership/module-http-initial.log)、[最终 race](evidence/2026-09-11-f01-connection-ownership/race-http.log) |
| SaaS 两跳 HTTP | 产品 HTTP → SDK Remote → 服务令牌验证 → Integration SQLite；同一用户分动作控制共享读取／撤销；两个用户、错误工作区、伪造参数、有效的 Identity owner deny、安全诊断、两次完整重启及撤销重放；匿名请求不能访问服务端口 | [最终有效策略 race，10.06 秒](evidence/2026-09-11-f01-connection-ownership/saas-policy-final.log) |
| Integration／SDK 回归 | 两个仓库 `go test ./... -count=1`；全量之后增加的撤销竞争与输入规范化通过上述最终专项 race；有效 deny 夹具单独补验 | [Integration 全量](evidence/2026-09-11-f01-connection-ownership/integration-full.log)、[SDK 全量](evidence/2026-09-11-f01-connection-ownership/sdk-full.log) |
| 静态与架构 | Integration／SDK `go vet ./...`；Integration 全量包含架构／持久化边界；Agent 应用层依赖方向检查；两仓库 `git diff --check` | [Integration vet](evidence/2026-09-11-f01-connection-ownership/integration-vet.log)、[SDK vet](evidence/2026-09-11-f01-connection-ownership/sdk-vet.log)、[Agent 边界](evidence/2026-09-11-f01-connection-ownership/agent-boundary.log) |

首次 SaaS 夹具错误地使用 `deny all`，Identity SDK 不接受该策略，返回 403；先修正状态预期后再次核对契约，最终改为合法 `deny owner` 并在发送前验证整个 AccessBundle，确认有效策略在两跳链路上拒绝当前账号。首次 SDK 全量测试还因批量替换误给 Web Push 查询断言加上账号参数而失败，已限定到账号辅助函数。失败日志保留：[SaaS 初次](evidence/2026-09-11-f01-connection-ownership/saas-http-initial.log)、[Integration 初次全量](evidence/2026-09-11-f01-connection-ownership/integration-full-initial.log)、[SDK 初次全量](evidence/2026-09-11-f01-connection-ownership/sdk-full-initial.log)。不把这些夹具错误当成产品缺陷证据。

## 范围与复现

测试采用真实 HTTP、磁盘 SQLite、Integration 实现、公开 SDK、确定性 Identity AccessBundle 和合成 Provider；**不是实际 Google／Microsoft OAuth，也不是 Agent 网页账号页面 E2E**。没有外部账号授权、发送消息、发布依赖或部署常驻服务。本批没有声称远端授权服务器已撤销用户 consent；当前撤销指 Integration 禁用连接和本地凭证。

复现时创建临时 Go workspace，仅包含现有 Integration 与 Integration SDK checkout（不新建分支或 worktree），再执行：

```sh
GOWORK=/tmp/domainry-f01-integration-ownership.work go test ./... -count=1
GOWORK=/tmp/domainry-f01-integration-ownership.work go test -race ./internal/infrastructure/persistence/database/integration ./internal/assembly/saas ./module -run 'TestConnectionAccount|TestPublicConnectionAccounts|TestPublicModuleRetainsRotatedCredentials' -count=1 -v
```

第二条在 Integration 仓库运行。当前仍用本地 SDK 增量，脱离 go.work 的版本发布与验证属于 H04。下一步严格继续 F01 首次 OAuth 的 Provider 协议、state／PKCE、持久回调与错误恢复，再做账号页面与工具开关。
