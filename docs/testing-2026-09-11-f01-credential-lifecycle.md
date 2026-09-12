# F01 凭证刷新并发与后台生命周期增量

2026-09-11。继续按 TODO 的 F01 顺序完成凭证生命周期审查。本批覆盖同步调用、投递、连接测试、后台任务和结果核对五条 Provider 执行路径，解决迟到刷新覆盖新凭证、撤销期间重新激活凭证、调用取消后丢失已经取得的刷新值，以及部分 OAuth Provider 在后续请求失败时丢弃 `SecretUpdates` 的问题。

本批不包含首次 OAuth 授权、个人／工作区连接归属、账号管理页面或工具开关，**F01 仍不勾选，整体仍为 49 / 81**。机器可读范围、源码哈希与日志哈希见[机器证据](evidence/2026-09-11-f01-credential-lifecycle.json)。

## 代码边界

- Integration 在解析密文凭证时同时取得字段对应的 secret key、指纹和更新时间快照。Provider 返回刷新值后，Integration 在一个宿主数据库事务中重新检查凭证仍为活动 material、引用未变化且版本与调用前一致，再原子更新元数据和密文材料。
- 迟到的并发刷新不能覆盖先提交的新值；调用期间发生撤销时，迟到结果失败且不能把状态改回 active。一个 Provider 同时返回多个字段时，其中任一字段版本不匹配会回滚整组更新。
- Provider 已完成刷新后，即使原请求 Context 随后取消，Integration 使用 `context.WithoutCancel` 和 5 秒独立上限完成有限保存。保存失败仍作为执行失败返回，不无限脱离调用生命周期。
- 后台任务与核对任务现在和同步路径使用同一快照／条件写入逻辑。业务结果为失败或 Provider 同时返回错误时，只要携带了已完成的刷新值，仍先尝试保存；业务错误和保存错误通过 `errors.Join` 同时保留。
- 没有新增公共 SDK 类型、数据库表或迁移。Agent 不保存外部凭证，也没有导入 Integration／Connector 的内部包。Integration 写入继续使用 ORM builder 和宿主 `Database.BeginTx`；Connector 仍只返回 SDK 定义的私有 `SecretUpdates`。

相关代码：Integration [条件保存入口](../../domainry-integration/internal/infrastructure/persistence/database/integration/provider_secret_updates.go)、[密文事务与版本检查](../../domainry-integration/internal/infrastructure/persistence/database/integration/secret_resolver.go)、[后台与核对路径](../../domainry-integration/internal/infrastructure/persistence/database/integration/provider_worker_store.go)；Connector [Google Workspace 同步与连接测试](../../domainry-connectors/providers/google_workspace/google/operations.go)、[Google Workspace 后台任务](../../domainry-connectors/providers/google_workspace/google/background.go)。

## Provider 审查

按 Catalog 中声明 OAuth refresh 的日常工作 Provider 逐项检查直接连接测试和后台返回值：

| Provider | 结果 |
| --- | --- |
| Freee、Money Forward、QuickBooks、Google Calendar | 已有路径会在后续请求失败时保留刷新值，无生产修改 |
| Microsoft 365 | 上一增量已修复，revision `1.0.1` |
| Google Workspace（Calendar／Drive／Gmail） | 修复直接连接测试及 Gmail 后台多步请求的失败分支，revision `1.0.1` |
| Microsoft Booking、DocuSign | 修复直接连接测试失败分支，revision `1.0.1` |
| FedEx、UPS | 修复直接连接测试成功和失败分支，revision `1.0.1` |

`providers/collaboration/google_workspace` 是 Google Chat 专用 Provider。它把 access token 标为 OAuth refresh，却没有 refresh token、client 配置或刷新实现，因此没有纳入本批“返回更新值”验证，也不能用于 F01 后续 Google 日常工作授权；F01 继续使用完整的 `providers/google_workspace/google`。该元数据差异将在首次 OAuth 契约接入时一并收敛，不能据此宣称 Google Chat 已有刷新能力。

## 验证

| 验证 | 实际覆盖 | 结果 |
| --- | --- | --- |
| 条件写入专项 | 新旧快照竞争、撤销期间刷新、两字段原子回滚、取消后有限保存、实际同步调用并发顺序 | [通过](evidence/2026-09-11-f01-credential-lifecycle/secret-concurrency.log) |
| 后台与核对专项 | 后台失败携带刷新值、后台执行中撤销、失败核对结果携带刷新值、Provider 错误同时返回结果 | [通过](evidence/2026-09-11-f01-credential-lifecycle/worker-refresh.log) |
| 生命周期组合回归 | 原 15 场景加并发、撤销、取消、后台与核对，公开 Module 关闭／重开数据库后继续使用刷新值 | [通过](evidence/2026-09-11-f01-credential-lifecycle/lifecycle-targeted.log) |
| Google Workspace | 类型化调用、直接连接测试、Gmail 后台任务均模拟 401 → 刷新成功 → 后续 503；检查新 token 已使用且公开 Payload／Details 无凭证 | [通过](evidence/2026-09-11-f01-credential-lifecycle/google-workspace-refresh.log) |
| OAuth Provider 审查 | Google Workspace、Microsoft Booking、DocuSign、FedEx、UPS 的完整包测试及新增失败场景 | [通过](evidence/2026-09-11-f01-credential-lifecycle/oauth-provider-audit.log) |
| Integration 完整回归 | `go test ./... -count=1`，含 Module、SaaS、持久层与架构测试 | [通过](evidence/2026-09-11-f01-credential-lifecycle/integration-full.log) |
| 竞态检查 | 新增生命周期场景及公开 Module 重启场景使用 `go test -race` | [通过](evidence/2026-09-11-f01-credential-lifecycle/integration-race.log) |
| 十个 OAuth Provider 包 | 所有已实现刷新逻辑的目标 Provider 包使用 `-count=1` 完整执行 | [通过](evidence/2026-09-11-f01-credential-lifecycle/oauth-providers-full.log) |
| 静态和生成一致性 | Integration `go vet ./...`；目标 Provider `go vet`；两个仓库 `git diff --check`；Connector 架构／Catalog 测试及两个生成器 `--check` | [Integration vet](evidence/2026-09-11-f01-credential-lifecycle/integration-vet.log)、[Connector 边界](evidence/2026-09-11-f01-credential-lifecycle/connectors-boundary.log)，全部退出码为 0 |

测试使用合成 Provider、确定性 HTTP Transport、临时磁盘数据库和测试 Cipher，没有访问真实 Google、Microsoft、物流或财务账号，也没有发送外部消息。真实授权和网页 E2E 留在 F01 后续步骤。

## F01 接续顺序

1. 在公共契约上实现个人／工作区连接归属、当前用户授权、撤销与安全展示字段；不能把 `CreatedBy` 当授权条件，也不能给普通用户 Integration 全数据管理权限。
2. 补首次 OAuth 授权 URL、state／PKCE、回调换 token 和错误恢复；Integration 管连接与密文，Provider 管外部协议，Agent 只经公开端口引用连接。
3. Agent 接入账号页面和每用户工具开关，执行、恢复和后台继续前重查当前连接与 Identity 权限。
4. 完成真实账号、双用户／跨工作区、刷新／撤销／重启、工具禁用和网页整段 E2E 后勾选 F01，再进入 F02。
