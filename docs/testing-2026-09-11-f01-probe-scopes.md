# F01：连接测试的 OAuth 范围验收（2026-09-11）

本批修复固定账号测试与实际授权范围不一致的问题。Google Provider 的测试调用 UserInfo，原页面默认只申请日历只读范围，旧夹具却无条件返回成功。现在凭证配置状态与测试条件分开：仅获准日历范围的账号继续满足对应业务范围，账号测试在服务端被拒绝，页面显示所需范围。不会自动扩大用户授权。

依据 Google 的[官方接口定义](https://www.googleapis.com/discovery/v1/apis/oauth2/v2/rest)，UserInfo 接受 `openid`、`userinfo.email` 或 `userinfo.profile`。Microsoft 的[Get user 文档](https://learn.microsoft.com/en-us/graph/api/user-get?view=graph-rest-1.0)列出 `/me` 的委托读取权限及更高权限；Provider 接受短名称与 Graph 完整名称。范围满足仅允许发起探测，远端仍可拒绝过期、撤销或受其他策略限制的授权。

## 实现与架构

- Connector SDK 增加可选 `OAuthConnectionTestScopeProvider`。外层为任一组合，组合内全部范围必须满足；显式空组合表示不需要 OAuth 范围，未声明保留旧行为。Registry 在注册时及读取时复制两层数组。旧 OAuthAuthorizer 接口不变。确定性契约更新为 v15，SDK 标识 dev.16；外部 Module 编译覆盖新接口。
- Google Workspace 与 Microsoft 365 Provider 分别声明自己固定测试接口的要求，修订升为 1.0.3；同步生成 Catalog。Provider 不互相导入，不引入 Integration 或产品实现。
- Integration 领域层纯函数计算测试条件；持久层读取 Integration 自有的实际 OAuth grant，并填充安全 DTO 的 `readiness.test`。缺少所需范围、旧账号无范围记录或无效 Provider 声明均阻止测试，发生在 Provider I/O 之前。`readiness.available` 仍表示本地凭证状态，不被测试范围替代。
- Agent 共享账号页面仅消费 Integration 浏览器 SDK 的状态；Tools 继续通过公开账号状态核对自己的业务要求。应用层没有新增跨服务实现依赖。未增加表、数据库连接池、后台服务或迁移账本。

## 验收证据

原始证据目录：[2026-09-11-f01-probe-scopes](evidence/2026-09-11-f01-probe-scopes/)。机器清单：[源码、构建与证据哈希](evidence/2026-09-11-f01-probe-scopes.json)。旧增量记录保留为当时快照，不冒充本批源码的验证。

- SDK／Integration SDK、Integration 持久层与领域层、两个 Provider 完整包通过：[owners-initial.log](evidence/2026-09-11-f01-probe-scopes/owners-initial.log)。SDK 覆盖外部模块编译、批量／单独注册、源数组与返回数组修改隔离、未声明兼容；领域规则覆盖 9 种组合及无效声明；相关 race 通过：[race.log](evidence/2026-09-11-f01-probe-scopes/race.log)。
- 实际 Identity HTTP 归属、共享权限及重启回归通过，1.21 秒：[identity-initial.log](evidence/2026-09-11-f01-probe-scopes/identity-initial.log)。测试凭证现在按每个 token 绑定实际授予的 scopes，UserInfo 夹具拒绝缺少资料范围的 token。
- 当前构建的 Agent 账号网页 **9 个场景**通过，11.60 秒；3 次换码、1 次远端探测、JavaScript 错误 0、敏感 Referer 0。覆盖管理员配置、授权回调、个人隔离、拒绝授权、切换身份、换码响应丢失、撤权／重启、账号撤销及手机布局、新增日历范围不足路径。最后一条直接 POST 返回 400 且探测计数不变，重启后仍保持业务凭证可用／测试禁用。见[日志](evidence/2026-09-11-f01-probe-scopes/browser-initial.log)、[浏览器报告](evidence/2026-09-11-f01-probe-scopes/browser-initial/report.json)、[宿主计数](evidence/2026-09-11-f01-probe-scopes/browser-initial/host-audit.json)、[已目视检查的手机截图](evidence/2026-09-11-f01-probe-scopes/browser-initial/scope-limited-mobile.png)。
- 请求 read/write、实际只授予 read 时，测试不能因请求过 write 而放行；无范围记录和无效声明都不调用 Provider：[partial-grant.log](evidence/2026-09-11-f01-probe-scopes/partial-grant.log)。Tools 实际目录、调用与重启测试明确验证同一账号的资料探测被拒绝后，满足日历范围的工具仍可用：[tools-probe-separation.log](evidence/2026-09-11-f01-probe-scopes/tools-probe-separation.log)。两项带 race。
- Agent／Integration／Connectors 全量通过：[full.log](evidence/2026-09-11-f01-probe-scopes/full.log)。其中 Agent Web 115.814 秒，Integration SaaS 12.691 秒，Integration 进程测试所在包 3.442 秒。全量之后仅加强上述两个测试断言，分别重新通过专项 race。Work／PM Go 回归：[products.log](evidence/2026-09-11-f01-probe-scopes/products.log)。
- Integration 浏览器 SDK 3 项测试通过：[browser-sdk.log](evidence/2026-09-11-f01-probe-scopes/browser-sdk.log)。Agent／Work／PM 前端构建分别 9.38／16.21／16.30 秒：[Agent](evidence/2026-09-11-f01-probe-scopes/frontend-build.log)、[Work](evidence/2026-09-11-f01-probe-scopes/work-build.log)、[PM](evidence/2026-09-11-f01-probe-scopes/pm-build.log)。本批 Work／PM 验证 Go 回归和共享页面编译，未重跑它们的浏览器场景；上一增量有各 3 场景的独立证据。
- 相关 vet、Connectors 依赖边界／Catalog／生成器及五个仓库 diff 检查通过：[vet](evidence/2026-09-11-f01-probe-scopes/vet.log)、[边界与生成器](evidence/2026-09-11-f01-probe-scopes/catalog-boundary.log)、[diff 检查](evidence/2026-09-11-f01-probe-scopes/diff-check.json)。

## 保留的失败与范围限制

[SDK 初始失败](evidence/2026-09-11-f01-probe-scopes/sdk-initial.log)保留两项夹具／契约更新错误：新契约哈希必须包含反射字段材料，不能只计算声明文本；Registry 测试 Provider 必须绑定至少一个操作。修正后完整 SDK 与 race 均通过。没有删除早期日志或用重试覆盖失败证据。

本批使用真实 Identity、Integration 持久化及官方 Provider，厂商授权／换码／UserInfo 为明确隔离的本地协议夹具；不构成真实 Google 或 Microsoft 账号授权、刷新与撤销的验收。用户已说明没有现成厂商配置，服务的管理员配置入口与运行参数已交付；厂商应用登记、可授权账号仍是外部条件。F01 保持未勾选，进度保持 49 / 81，不进入 F02。当前进程测试与浏览器测试都已退出，没有新增常驻服务。

## 复现

在 Agent 根目录，使用当前 go.work 中的同级库。先运行 `npm --prefix ../domainry-integration-sdk/browser test`，再构建 `npm --prefix frontend run build`。账号浏览器测试设置 `AGENT_ACCOUNTS_BROWSER=1`、`AGENT_NODE_BINARY`、`AGENT_PLAYWRIGHT_MODULE` 和绝对 `AGENT_UI_TEST_OUTPUT`，执行 `go test ./internal/assembly/web -run '^TestExternalAccountsBuiltBrowser$' -count=1 -v`。仅在各同级仓库内直接执行命令时，显式设置 `GOWORK=/Users/tiger/Projects/domainry-agent/go.work`，避免解析到已发布的旧 SDK。
