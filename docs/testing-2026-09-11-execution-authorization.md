# C03 当前执行主体授权增量

日期：2026-09-11。本批按 TODO 顺序完成 C03：网页、Runtime Module 和独立 SaaS 的当前主体检查、恢复与确认继续、后台领取及冻结来源复核已有实现与验证。未扩展后面的资料共享、调度或业务操作范围；计划触发产品仍在 G 项，不能以现有后台 worker 通过替代调度交付。

## 代码依据和修改

原有工具、确认、资料库成员及来源复核已经使用当前权限；缺口在纯文本／空工具目录／摘要路径：worker 从存储读取旧 Authority 后，没有单独检查用户是否仍是有效主体。另一个缺口是明确恢复及答复后没有更新 `authority_json` 中的角色选择，导致当前请求已获准，后续 worker 仍使用旧角色。

- SDK `conversation_authorization.go` 定义 `ConversationExecutionAuthorizer`。请求只有可信主体、会话／Run 标识和应用指定的阶段，不包含 Cookie、访问令牌、旧权限包或模型提供的授权参数。
- Agent `internal/application/conversation_authorization.go` 经该端口检查发送、恢复、答复后继续、worker 领取、摘要、模型、工具及正式回复提交。每次最多 5 秒；拒绝和验证故障分别保存固定错误码，不保存任意宿主错误。拒绝发生在入队／恢复／答复入口时，不提交对应消息或状态变化。
- `module/conversation_assembly.go` 要求绑定的应用宿主提供执行授权，或使用显式 `ConversationOptions.ExecutionAuthorizer`；缺少时不能完成绑定和启动 worker。显式执行策略优先，显式工具策略不阻断从实际宿主取得独立执行策略。
- 网页 `internal/assembly/web/conversation_tools.go` 抽取现有 Identity 主体解析，同时用于执行准入与原工具动作评估。当前 SDK 的 send／resume Action 是 authenticated，没有单独角色权限；实现沿用这一契约。已知身份拒绝与服务故障分开，二者都不继续执行。
- Runtime `runtime/application/agenthost/conversation_business_host.go` 实现同一公开端口，复用其当前主体解析。没有引入 Agent 应用层到 Runtime 或 Identity 实现层的直接依赖。
- 独立 `cmd/domainry-agent` 使用 `internal/assembly/saas/conversation_identity.go` 经公开 Identity SDK 完成发现、契约校验及当前主体解析。启动绑定固定租户／工作区／应用；会话 HTTP 额外限定工作区，凭证不进入 Run。缺失或不兼容的绑定在 worker 启动前失败。非延迟 Module 有模型时同样必须提供执行授权端口，不能借立即装配绕过要求。
- 原存储 `conversation_run_store.go`、`conversation_interaction_store.go` 在明确恢复／首次有效答复的事务内保存当前 Authority，CAS 仍用原所有者／fence／状态／事件序号，拒绝改变所有者。现有字段已足够，不新增表或迁移账本；写入仍通过 ORM。原消息、写范围、参数摘要和工具回执保持原值，重复答复不更新身份或重复执行。
- `frontend/src/errors.ts` 为执行主体失效和授权服务暂不可用增加可读错误说明。

当前资料权限仍是“个人资料＋共享资料库”：reader／editor／manager，文档继承库权限，不使用组织树继承。资料库成员变化、文档状态及冻结来源的实时复核沿用现有实现，参见 [权限模型](knowledge-permissions.md)。新执行准入不能代替任何工具或资料授权。

## 验证与范围

| 验证 | 结果与证据 |
| --- | --- |
| 发送／执行／模型／摘要／提交拒绝与错误脱敏 | `integration/conversation_authorization_integration_test.go`；拒绝发送不落消息，后台拒绝不调用模型，摘要单独检查，提交前拒绝不产生正式 assistant 消息，宿主原始错误不落 Run。 |
| 冻结输入恢复与角色变化 | 同一测试文件；实际关闭服务、保留旧冻结输入、重新领取时撤权阻止模型；拒绝 Resume 不改变 Run。授权恢复后用新角色继续同一 Run。 |
| 确认后继续与角色变化 | 同一测试文件；分别在答复、重新领取、工具、下一次模型处撤权。已完成的一笔写效果保留，后续模型不执行；当前角色随首次确认提交，重复答复不产生第二笔效果。 |
| 薄宿主绑定 | 同一测试文件；只有旧工具策略的应用宿主不能绑定，补齐后可绑定；显式拒绝策略不会被替换。既有 A08 延迟装配测试同时通过。 |
| 角色更新与 B06 取消竞态回归 | `/tmp/domainry-C03-role-cancellation-race.log`；Integration 与真实存储 race 通过，包含迟到回执、取消／恢复、确认重复和角色更新。 |
| 资料／来源／确认专项 | `/tmp/domainry-C03-execution-source-race.log`；执行、冻结知识输入、来源摘要重建、确认撤权及知识服务用例通过，17.249 秒。全量测试另外覆盖资料库成员、历史／结果嵌套来源及重启。 |
| 实际 Identity 停用与恢复 | `internal/assembly/web/conversation_authorization_test.go`；使用真实 Identity 模块、管理 HTTP、产品 HTTP 和 SQLite，仅调度闸门与模型为夹具。入队后通过管理员停用实际测试账号，再放行 worker；模型调用增量为 0，失败码为 execution_access_denied。原登录会话返回 401；启用账号、重新登录及明确恢复后完成。`/tmp/domainry-C03-identity-race.log`，9.925 秒。 |
| Agent／SDK 全量与静态检查 | `/tmp/domainry-C03-agent-final.log`、`/tmp/domainry-C03-sdk-full.log`、`/tmp/domainry-C03-vet-final.log`；均通过。Agent 全量包含应用层不可导入具体适配器／其他服务实现的架构测试。 |
| Runtime 当前宿主与 HTTP | `/tmp/domainry-C03-runtime-race-local.log`：当前主体、跨 Runtime／工作区／用户和撤权，以及原会话业务宿主 race 通过。`/tmp/domainry-C03-runtime-http.log`：业务读取、关联、动作和实际 Workflow HTTP／重启／撤权四项通过，50.501 秒。角色持久化修改后补验实际 Workflow，`/tmp/domainry-C03-runtime-http-final.log` 6.621 秒通过。 |
| 外部身份模式回归 | `/tmp/domainry-C03-external-identity.log`；`external_identity` 构建下账号隔离与重启测试通过。上游账号验证服务为协议夹具，未声称验证实际外部账号停用同步。 |
| 前端 | `/tmp/domainry-C03-frontend-tests.log`：25 项通过；`/tmp/domainry-C03-frontend-build.log`：TypeScript／Vite 通过，保留原有 chunk 大小提示。 |

Runtime 默认依赖已发布的旧 SDK，因此最初普通命令未能编译新增契约，日志 `/tmp/domainry-C03-runtime-race.log` 保留。后续按已有业务测试脚本的方式，用 `/tmp/domainry-C03-runtime.work` 组合 Agent、Agent SDK、Connectors、Identity、Identity SDK、Runtime 本地源代码；不改 Runtime 的发布依赖。上述 Runtime 结果均来自这组本地契约，**不能宣称旧版本 SDK 已支持新接口**。SDK 发布与依赖升级仍归 H 项。

测试编写期间修正了测试所用事件字段、实际账号 ID 和 Resume 的 HTTP 200 契约。初次尝试也暴露 send 尚未声明角色权限，最终按已有 authenticated Action 实现，没有通过放宽权限或自动授予新权限来使测试通过。

## 浏览器端到端

脚本 [authorization.browser.mjs](../frontend/tests/authorization.browser.mjs) 启动独立的无头 Chrome，访问编译后的产品 UI。测试首先校验 admission-workspace，使用临时数据库和合成账号，不改正在部署的用户或权限。

首轮 `/tmp/domainry-C03-browser.log` 与宿主日志 `/tmp/domainry-C03-browser-host.log` 通过；宿主完整测试 20.09 秒。4 个场景为：

1. 当前用户在网页创建会话和发送消息，实际持久 Run 到达 worker。
2. 管理 HTTP 停用账号后再放行，后台模型调用为 0；浏览器会话和会话读取均返回 401。
3. 账号启用、重新登录后显示原 Run 的持久失败说明；只有原用户消息，没有 assistant 回复。
4. 关闭并重建完整宿主，Run 仍失败；用户明确点击“重新生成”，同一 Run 从 attempt 1 到 2，最终只新增一条正式回复。

角色更新后的最终复验通过，宿主完整测试 21.57 秒，4 场景、JavaScript 错误 0；Run `crun_6e06ef58037bb95cd81efb55244789c4` 最终在 attempt 2 完成。日志为 `/tmp/domainry-C03-browser-final.log`、`/tmp/domainry-C03-browser-host-final.log`，报告和截图在 `/tmp/domainry-C03-browser-final/`。原报告仍在 `/tmp/domainry-C03-browser/`。JavaScript 错误、状态、消息数量和同一 Run 的恢复由脚本断言；截图另外查看错误说明是否可读。测试退出码均为 0，临时监听器已关闭。

可复现命令（模型与业务内容均为夹具，不需要真实服务密钥）：

```sh
npm --prefix frontend run build
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run TestConversationCurrentIdentityAdmissionAndBrowser -count=1 -v -timeout 8m
# 等待测试提示 8092 已就绪，在另一终端运行：
AGENT_PLAYWRIGHT_MODULE=/absolute/path/to/playwright node frontend/tests/authorization.browser.mjs
```

## 独立 SaaS 补齐与最终验证

`cmd/domainry-agent/conversation_identity_test.go` 通过实际可执行程序装配入口、Agent HTTP、两个公开 remote SDK、SQLite 和 Identity 协议夹具验证：入队后撤权、应用服务凭证失效、错误主体响应均不能调用模型；恢复拒绝不修改原运行，恢复权限后同一 Run 完成。跨 Runtime／工作区被拒绝，缺少 Identity 配置不会领取已入队的旧任务。模型使用合成 SSE，不调用真实 Verdent。

`cmd/domainry-agent/identity_process_test.go` 另行构建并启动相邻仓库的真实 Identity 可执行程序，使用临时 SQLite、回环地址及合成账号凭证。实际登录并修改初始密码，通过管理员 HTTP 停用／启用账号；代理只暂停一次主体请求，不替换 Identity 响应。Agent 经真实 HTTP 和 SDK 完成发送、失败查看及恢复。测试通过真实仓储另行植入一笔已冻结输入、即将过期的旧租约，重开 Agent 服务验证后台恢复时重新检查主体；这不是另一个 Agent OS 进程的崩溃测试。

| 验证 | 最终结果与证据 |
| --- | --- |
| 实际 Identity 独立进程 | `/tmp/domainry-C03-saas-real.log`，4.738 秒通过；账号停用后普通排队和冻结租约恢复的模型调用均为 0；启用并明确恢复后 Run `crun_3605a8b454abeb78e535b23aee194bfd` 在 attempt 2 完成，模型总调用 1 次、主体解析 8 次，消息数 2。子进程日志 `/tmp/domainry-C03-saas-real/identity-process.log`；测试退出时清理子进程和临时数据库。 |
| 启动与恢复 race | `/tmp/domainry-C03-saas-final-race.log`；可执行程序包 5.753 秒、Integration 3.411 秒通过，含缺失策略的立即 Module 装配。协议夹具专项另见 `/tmp/domainry-C03-saas-race-fixed.log`。 |
| 最终 Agent 全量 | `/tmp/domainry-C03-agent-saas-full.log`；所有包通过，包含实际网页 Identity 宿主回归及应用层架构边界检查。 |
| 最终静态检查 | `/tmp/domainry-C03-saas-vet.log`；`go vet ./...` 退出码 0。SDK、Runtime 和前端没有在该 SaaS 补齐中再次修改，沿用上方已通过的对应检查。 |

独立进程验收可复现命令（需要相邻 Identity 源码和现有本地 go.work，不读取开发者实际服务凭证）：

```sh
AGENT_IDENTITY_PROCESS_ACCEPTANCE=1 AGENT_C03_EVIDENCE_DIR=/tmp/domainry-C03-saas-real \
  go test ./cmd/domainry-agent -run '^TestSaaSConversationAgainstRealIdentityProcess$' -count=1 -v -timeout 5m
```

最初独立进程测试将 `IDENTITY_APPLICATION_SERVICE_CREDENTIALS` 误设为 JSON，发现接口可读但 capability 校验返回 `module_capability.service_credential_required`。按 Identity 当前配置解析代码改为 `workspace/application=token` 后通过；没有放宽服务凭证校验或修改 Identity 源码。

## 边界与后续

内部 `NewConversationService` 在端口缺失时仍保留旧内部调用契约；第一方生产组合入口已分别强制绑定策略。旧 playground 无生产 Identity，只允许监听字面量回环 IP，不作为生产部署方式。定时／计划触发上线时必须经同一执行准入并按 G 项验收；SDK 发布与宿主依赖升级仍在 H 项，当前 Runtime 验证使用本地 SDK 工作区。

C03 完成后下一项为 C04 的具体操作授权范围。K04／K07 继续按已确认的个人资料＋共享资料库实现，跨库移动、远端 ACL 和真实多库验收仍保留未完成。
