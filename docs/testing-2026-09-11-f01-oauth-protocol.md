# F01 首次 OAuth 与独立 Integration 进程增量

本记录仅完成 F01 的首次 OAuth 后端与宿主运行入口，不勾选 F01、不进入 F02。用户确认没有现成 OAuth 应用配置，因此配置、授权会话和密钥保管均由 Integration 服务实现。Google／Microsoft 后台签发的有效客户端凭据仍需对应账号的实际应用注册；本批不伪造真实外部验收。

## 架构与实现

- Connector SDK 增加可选 `OAuthAuthorizer`；Registry 冻结后仍能解析该能力，不扩大原 Adapter 必选接口。Go 外部消费编译验证通过。契约为 `connector-contract-v14`，哈希 `73d55fb89cd78a82819a4ce681ddf576a1867c56748ea0b0fb4539781ca4333c` 以机器证据及 SDK 常量为准。
- 复用 Connectors 的 Google Workspace／Microsoft 365 Provider，新增授权码＋S256 PKCE 协议。公共 OAuth 协议在 Connectors 内部复用，Provider 之间不相互导入；所有 token 请求经过宿主 SDK Transport。既有 Google Chat 没有刷新实现，其错误的自动刷新声明已改为手工更新，未宣称 Chat OAuth 已接通。
- Integration SDK 提供应用配置、当前用户授权目录、发起、查询与完成端口；管理员配置只写 client secret，普通用户目录不返回 client ID、Provider 配置或密钥。授权回调始终绑定真实当前工作区／用户及该 Action 的数据范围，浏览器不能指定可信主体或提升共享范围。
- Integration 使用宿主唯一迁移账本增加 v5 两张表，持久化应用修订、状态哈希、加密 PKCE verifier、期限和会话回执。配置变更要求旧修订。开始授权只允许选取该应用已配置的明确 scopes。
- 换码前原子领取一次性会话；领取后有 45 秒有限完成窗口，不随浏览器断开丢弃已返回的 token。成功后同一事务生成独立密文、连接和不可转让的账号归属。实际 granted scopes 随授权回执保存；重复回调只读原回执，重新查询能反映账号已撤销状态。
- 明确拒绝为 `rejected`；响应不明或没有 refresh token 为 `needs_reauthorization`，不自动重放 code。超过交换期限的遗留会话也要求重新授权。state／code／verifier／token 均不写入模型与日志；Get 不返回授权导航 URL。
- 独立 Integration 进程通过 Connectors 的公开 `module.WorkAccountProviders` 装配两个 Provider，不导入 `providers/**`。Integration 宿主实现 AES-256-GCM，密文绑定工作区及材料 key；明确要求持久化的 `INTEGRATION_MASTER_KEY`，不会自动生成重启即失效的替代密钥。
- 独立宿主 HTTP 传输只允许两个厂商的官方 HTTPS API 域名，注入私有 Header／Form／Query／JSON，拒绝字段冲突、跳转、超限响应，不进行 OAuth 应用级重试。Module 宿主仍通过 SDK 提供自己的传输与加密。

代码入口：[Connector SDK OAuth](../../domainry-connector-sdk/oauth.go)、[Connector 公共装配](../../domainry-connectors/module/module.go)、[通用 OAuth 协议](../../domainry-connectors/internal/oauth2/authorization.go)、[Integration OAuth SDK](../../domainry-integration-sdk/oauth.go)、[应用配置](../../domainry-integration/internal/infrastructure/persistence/database/integration/oauth_application_store.go)、[授权会话](../../domainry-integration/internal/infrastructure/persistence/database/integration/oauth_session_store.go)、[独立进程](../../domainry-integration/cmd/integration-server/main.go)。

## 已执行验证

| 层次 | 直接证据及结果 |
| --- | --- |
| SDK 与协议 | Connector SDK 全量包含外部编译；Google／Microsoft 与通用 OAuth 包验证 PKCE、固定 flow、私有材料、实际 scopes、已拒绝与结果不明分类。Integration SDK Go 全量通过。 |
| 持久化与并发 | 5 个 OAuth 场景覆盖 owner／workspace／scope／state 隔离，到期、拒绝、未知结果，配置变更回滚，12 个并发回调只换码一次及取消后的原子提交。专项 race 通过。 |
| Module 与 SaaS HTTP | 实际 Google Provider＋宿主 Transport＋本地 token HTTP 服务校验 PKCE。两种模式均验证配置权限、跨用户拒绝、两次完整宿主关闭重开、重复回调无第二次请求、503 后无重试、账号撤销后回执更新。race 总场景 16.47 秒（Module 6.13 秒，SaaS 10.35 秒）；包耗时 22.767 秒。 |
| 独立真实进程 | 实际执行 `run()`，同一个 SQLite 连续启动三次。保存 Google 和 Microsoft 配置，发起 Google 官方导航 URL，重启查询 pending，再拒绝授权，再重启查询 rejected。服务令牌缺失拒绝；数据库无明文 client secret／state。完整场景 1.04 秒，最终包 1.491 秒；进程／加密／网络 race 均通过。 |
| 浏览器 SDK | TypeScript 编译和 3 项客户端测试通过；覆盖全部 48 个浏览器路由方法、URL 编码、固定请求方法与载荷、取消信号、响应元数据。这里不是 Agent 页面浏览器 E2E。 |
| 架构与兼容 | Integration 全量与 vet 通过；Connector 架构、Catalog、两个生成器一致性检查通过。Connectors 全量／vet 与 Agent 全量通过；随后补齐可选 OAuth 能力声明，Binding／SaaS／真实进程与 SDK remote 复验通过。五个仓库 diff 检查通过。 |

原始日志集中在 [OAuth 证据目录](evidence/2026-09-11-f01-oauth-protocol/)，文件哈希与命令见 [机器证据](evidence/2026-09-11-f01-oauth-protocol.json)。

失败也保留：首次协议测试写错 SDK 错误常量；Google Chat 修订断言未更新；浏览器初次缺少 tsc，安装锁定依赖后发现类型导出来源错误，已修复并补客户端测试；独立传输测试的 TLS 夹具使用错误服务名，已改为夹具证书中的 example.com，未降低生产 TLS 校验；进程测试初次写错 scope 常量、其次未提供明确 scopes，均修正测试输入后通过。对应 initial／final／verified 日志没有覆盖删除。

## 范围与接续

本批上游 token issuer 为本地协议服务，Identity HTTP 验证使用合法确定性 bundle。真实进程验证走官方授权 URL 构造和用户拒绝回执，没有调用真实 Google／Microsoft 换码接口。尚未有实际厂商登录／同意授权、Agent 账号页面、浏览器回调与工具开关验收，F01 继续不勾选。

SaaS 服务端接口只供持服务令牌的可信产品宿主调用。产品页面需接收 OAuth 导航回调、先清除查询中的 code／state，再通过经过 Identity 的同源回调 API 完成；API 本身不是匿名回调页面。撤销目前停止 Integration 本地凭证使用，未冒称已删除厂商后台的 consent。

使用已有本地 checkout 和临时 Go workspace 验证，未创建分支或 worktree，未发布新 SDK tag、未改成 go.mod 本地替换、未部署常驻服务。跨库新契约发布仍按后续发布 TODO。

协议设计依据：[Google Web Server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server)、[Microsoft 授权码流程](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow)、[OAuth 安全最佳实践 RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)。分别用于核对授权 URL、PKCE、机密客户端换码与实际授予范围。
