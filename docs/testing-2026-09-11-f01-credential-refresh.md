# F01 外部连接复用与凭证刷新增量

2026-09-11。按 TODO 顺序进入 F01。本批修复复用链路中“令牌刷新成功、后续业务请求失败时丢失新凭证”的问题，尚未完成用户 / 工作区账号管理。**F01 不勾选，整体仍为 49 / 81。** [机器证据](evidence/2026-09-11-f01-credential-refresh.json)记录实际代码哈希、日志和验证范围。

## 当前代码核对

| 已有实现 | 可以复用的能力 | 当前缺口 |
| --- | --- | --- |
| Connectors `providers/appointment_scheduling/google_calendar` | 日历事件操作、连接测试、401 后刷新；令牌通过宿主 Transport 的私有字段传递 | 未提供浏览器首次 OAuth 授权 / 回调；不代表 Gmail 读取能力 |
| Connectors `providers/microsoft_365/microsoft` | Calendar、Contacts、OneDrive 文件引用、Outlook Mail 读取操作及连接测试、刷新 | 直接连接测试在刷新后的请求失败时丢掉 `SecretUpdates`；本批修复 |
| Integration v0.1.11 当前源码 | Workspace 连接配置、凭证引用、宿主密文存储、调用与投递记录、连接测试、状态和撤销 | 同步调用 / 投递只有业务成功才保存刷新值；连接测试完全未处理刷新值；本批修复这三条路径 |
| Integration SDK v0.1.4 | 公共 `Management`、`Operations`、`Delivery`、Module 宿主端口 | `Connection.CreatedBy` 是创建记录；当前管理存储没有个人连接数据范围，写操作要求全数据范围。不能直接把此管理接口当成普通用户的“我的账号” |
| Agent SDK `ConversationToolAvailability` | 工具目录与执行前检查当前连接 / 工具可用性，Module 可从宿主绑定 | 目前没有 F01 的账号授权页面、连接管理持久化及工具开关页面；此接口也禁止在可用性查询里刷新凭证 |

Integration 位于相邻的普通源码 checkout `domainry-integration`，本批基线为 `d5adee81f049b8dfedfafa8523df836fdf252671`。未创建新分支或 worktree，未修改 Agent 的 `go.mod` / `go.work`。两个服务继续使用公共 SDK 边界。

## 实际修改

- Integration 新增内部 `DeliveryStore.persistProviderSecretUpdates`：Provider 已经完成的令牌刷新独立保存，随后业务失败仍返回原错误；若保存也失败，通过 `errors.Join` 保留两个错误。未产生刷新值时维持原行为，缺少写入能力时明确失败。
- 同步 `OperationsStore.Call`、`DeliveryStore.Accept`、`ManagementStore.TestConnection` 接入同一个内部方法。刷新值依然交给 Integration 的 `SecretUpdateWriter` 和宿主 Cipher，公开结果不增加凭证字段；凭证更新失败也不会被报告成连接测试成功。
- Microsoft Provider 的直接 `ConnectionTester` 在错误分支保留刷新值，业务错误分类保持原值；Provider revision 升至 `1.0.1` 并重新生成 Catalog。Google Calendar 仅补回归测试，没有改生产代码。
- 没有新增数据库表、迁移账本或原始 SQL 写入。新增公开 Module 测试只使用 `module` 和公共 SDK，不导入内部存储；宿主通过 ORM Runner 使用唯一 `_schema_migrations`，真实关闭并重开磁盘 SQLite。
- 源文件解析、知识检索和查看器没有改动。Agent 没有新增凭证副本，也未把其他仓库实现导入应用层。

代码：Integration [统一处理](../../domainry-integration/internal/infrastructure/persistence/database/integration/provider_secret_updates.go)、[存储场景](../../domainry-integration/internal/infrastructure/persistence/database/integration/provider_secret_updates_test.go)、[公共 Module 验证](../../domainry-integration/module/credential_rotation_test.go)，以及 Connectors [Microsoft 修复](../../domainry-connectors/providers/microsoft_365/microsoft/provider.go)。

## 验证与证据

| 验证 | 实际覆盖 | 结果 |
| --- | --- | --- |
| 修复前回归 | Integration 三条路径的 9 个场景中 7 个失败；Microsoft 直接连接测试的 HTTP / 网络失败丢失更新，Google 和 Microsoft 类型化调用正常 | [Integration 红灯](evidence/2026-09-11-f01-credential-refresh/integration-regression-red.log)、[Provider 红灯](evidence/2026-09-11-f01-credential-refresh/providers-regression-red.log) |
| Integration 15 个场景 | 三种入口 × 业务成功 / 业务失败 / 两种存储失败组合 / 无写入能力；核对原错误、保存错误、回执状态、实际解密结果、密文、跨 Workspace 拒绝和公开结果无凭证 | [全部通过](evidence/2026-09-11-f01-credential-refresh/integration-regression-matrix.log) |
| 官方 Provider 回归 | Google Calendar 和 Microsoft：类型化调用 / 直接连接测试 × HTTP 503 / 网络断开；401 后成功刷新，共三次 Transport 调用，不增加重试；检查新令牌被用于后续请求和通过私有结果返回 | [两个 Provider 包通过](evidence/2026-09-11-f01-credential-refresh/providers-regression-green.log) |
| 公共 Module 3 条路径 | 使用公开 SDK 创建合成连接和密文凭证；刷新后业务失败，关闭 Binding / 数据库；新建宿主、Provider、Binding，继续请求直接使用新令牌；错误 Workspace、撤销后均在 Provider 前拒绝；核对宿主迁移账本和公开 JSON | [1.23 秒通过](evidence/2026-09-11-f01-credential-refresh/module-restart-green.log) |
| Integration 完整回归 | `go test ./... -count=1`，包含 Module / SaaS 和架构边界；未重复 Agent / 前端全量 | [通过](evidence/2026-09-11-f01-credential-refresh/integration-full.log) |
| race / vet / Catalog | 两个新增 Integration 场景的 race；Integration 全量 vet；两个 Provider 的 vet；Connectors 架构与 Catalog 测试及生成一致性 | [race](evidence/2026-09-11-f01-credential-refresh/integration-race.log)、[Integration vet](evidence/2026-09-11-f01-credential-refresh/integration-vet.log)、[Provider vet](evidence/2026-09-11-f01-credential-refresh/providers-vet.log)、[边界与 Catalog](evidence/2026-09-11-f01-credential-refresh/connectors-boundary-catalog.log) |

公共 Module 场景使用**可信宿主 SDK、合成 Provider、临时磁盘数据库和 AES-GCM 测试 Cipher**，并非真实外部账号授权或普通用户 Identity 验收。Provider 场景使用确定性宿主 Transport；没有访问真实 Microsoft / Google 账号，没有发送外部消息。

最初测试夹具缺少 PermissionKey、Provider 密钥生命周期描述及投递去重键；Provider 测试曾错误地把供宿主使用的内部结果整体当成公开 JSON，已改为检查 `Payload` / `Details`，SDK 私有结果仍正常携带刷新值。上述失败与最终成功均保留，不能把夹具初始化失败当作产品缺陷的复现。

可复现命令（分别在对应仓库运行）：

```sh
cd /Users/tiger/Projects/domainry-integration
go test ./internal/infrastructure/persistence/database/integration -run '^TestCredentialRotationPersistsIndependentlyOfProviderOutcome$' -count=1 -v
go test ./module -run '^TestPublicModuleRetainsRotatedCredentialsAcrossRestart$' -count=1 -v

cd /Users/tiger/Projects/domainry-connectors
go test ./providers/appointment_scheduling/google_calendar ./providers/microsoft_365/microsoft -count=1 -v
```

## F01 剩余工作，继续按此顺序

1. 凭证生命周期：已在后续[生命周期增量](testing-2026-09-11-f01-credential-lifecycle.md)完成后台／核对路径、刷新与撤销并发、多个刷新互相覆盖及调用取消后的有限保存边界；该增量保留独立机器证据。
2. 在公共契约上补齐用户 / 工作区连接归属、当前授权与撤销、首次 OAuth 授权 / 回调。连接与凭证生命周期应由 Integration 所属能力维护；Provider 负责协议，Agent 通过公开端口引用连接。不能用 `CreatedBy` 筛选或给普通用户全数据权限替代连接授权。
3. Agent 接入连接页面及工具开关，复用当前 Identity 授权和 `ConversationToolAvailability`，执行与恢复时重新检查当前连接。浏览器 / 模型只获得安全展示字段和不透明连接标识；凭证、授权码、PKCE 材料不进入会话消息或工具元数据。
4. 完成真实授权、双用户 / 跨工作区隔离、刷新 / 撤销 / 重启、工具禁用与网页整段验收后再勾选 F01，然后进入 F02。当前修复尚未发布依赖版本或部署到常驻网页服务。
