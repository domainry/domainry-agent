# C04 具体操作授权范围验收

日期：2026-09-11。按 TODO 顺序完成 C04，下一项为 E05。个人记忆／待办／成果继续支持用户随本次请求明确提交的原有分类授权；本批补上对已列出具体操作的组合授权，覆盖个人工具及业务动作。没有扩展资料库权限、外部账号或计划触发。

## 用户行为与边界

同一步模型结果已经完整冻结、包含多项需要确认的有效写操作时，确认卡片最多列出 20 项具体操作。用户可以“仅确认第一项”，也可以“授权并执行这 N 项”。列表使用现有业务、流程、待办、记忆和成果预览，展示实际目标及参数。刷新前尚未提交的选择不是授权；丢失响应后的重试沿用原响应编号和范围。

授权只涵盖列出的本次运行／步骤／调用、工具版本、动作、定义和完整参数，每项对应一个逻辑调用。范围内的后续调用直接使用已保存的授权凭据，重启或明确恢复时沿用调用账本，不再次询问或重复创建效果。后续模型增加一项同名动作、更换目标或参数、重新发起请求，均不继承原范围。不完整参数不能成为可批量确认的列表；保留原有参数校验反馈和 `ask_user` 补充信息链路。

Identity 动作、具体业务资源、来源、工具可用性及宿主额外审批要求继续逐次检查；批量授权不能授予权限、扩大个人资料或共享库范围，也不会回滚先前已完成的动作。对外部调用的不确定结果仍按 B04／B06 核查，不能把一次用户授权当作外部系统恰好执行一次的保证。

## 存储与架构

- Agent SDK 的 `ConversationInteraction.Operations` 是服务端构建的只读列表；`ConversationInteractionResponse.Scope=listed_operations` 表示用户明确选择该列表。默认空值维持单项确认。浏览器和模型不提供批准凭据或新的目标列表。
- `internal/application/conversation_operation_scope.go` 在生成预览及响应提交时核对当前目录、完整输入契约、每个动作及具体资源权限。未知、无权、定义变化或参数不完整时，不提供批量授权；提交时重新核对已有列表。
- `conversation_operation_scope_store.go` 从已冻结步骤重新推导目标，拒绝乱序、非剩余写调用及已启动目标。一个宿主数据库事务保存原响应、各项精确授权记录、来源授权编号、用户消息、事件和 Run 状态；任一步失败回滚。新增目标字段不会从用户响应拷贝入库。
- 子授权仍使用原 `ConversationInteraction`／`ConversationConfirmation` 契约，绑定实际参数摘要、工具版本和批准者。`AuthorizationID` 指向原组合确认，`ApprovedScope` 区分批准整个列表与只批准第一项。实际执行继续通过旧调用账本及稳定幂等键；不另建授权服务或第二个账本。
- 数据保存在原 `_agent_conversation_interactions`、Run 和事件的 JSON 中，不增加表或迁移账本。读写沿用宿主事务和 ORM。应用层只依赖公开 SDK 端口；Runtime 业务源码无需修改，只增加实际宿主验收测试。

## 验证与证据

| 验证 | 结果 |
| --- | --- |
| 业务执行及范围隔离 | `integration/conversation_operation_scope_integration_test.go`：批量确认后服务关闭／租约恢复、仅批准第一项、确认前／后的第二笔资源撤权、契约变化、拒绝、跨用户、参数不完整。新调用不能借用原授权；重复确认改变范围被拒绝；已完成第一笔不会因第二笔拒绝或恢复而重做。初始 7 场景 race 通过，`/tmp/domainry-C04-scope-race-fixed.log`，5.150 秒。缺参数补验见 `/tmp/domainry-C04-incomplete-parameters.log`。 |
| SaaS 真实协议往返 | 同一测试文件 `TestConversationListedOperationScopeOverSaaS`；通过 Agent HTTP 和 remote SDK 保留列表及响应范围，拒绝未知 scope，组合确认产生恰好两个夹具业务效果。权限和模型为夹具，不声称实际外部服务。 |
| 真实 Identity／HTTP／SQLite | `internal/assembly/web/conversation_operation_scope_test.go`：完整宿主重启前后的列表一致；一个用户响应创建两项实际待办，重复响应无额外效果；第三项再次确认，拒绝后没有第三项。`/tmp/domainry-C04-identity-http-fixed.log`，1.405 秒通过。 |
| 实际 Runtime 业务写入 | 相邻 Runtime 的 `runtime/bootstrap/integrationtest/conversation_operation_scope_web_test.go`。从真实业务目录取得动作契约，一次组合确认创建两个不同客户；Identity／Runtime 完整重启及重复确认后，每条记录仍只有一份。`/tmp/domainry-C04-runtime-http.log`，5.226 秒通过，Run `crun_4ff2c06e369f36ed39645c28b34f4273`。只有模型决策为夹具，业务调用、记录与 Identity 均为实际实现。 |
| 网页整段操作 | `frontend/tests/operation-scope.browser.mjs`，4 场景、JavaScript 错误 0。真实产品页、Identity、HTTP 和 SQLite；模型为夹具。`/tmp/domainry-C04-browser-final.log`、`/tmp/domainry-C04-browser-host-final.log`，宿主测试 48.938 秒；报告及截图 `/tmp/domainry-C04-browser-final/`。 |
| 最终 race | `/tmp/domainry-C04-final-race.log`：Integration 14.117 秒、实际网页宿主 26.692 秒、存储 2.759 秒通过；覆盖 Scope／SaaS、既有交互和取消回归。 |
| 全量及静态 | `/tmp/domainry-C04-full.log`、`/tmp/domainry-C04-sdk.log`、`/tmp/domainry-C04-vet.log` 均退出 0，包含应用层依赖边界测试。前端 25 项通过，`/tmp/domainry-C04-frontend.log`；TypeScript／Vite 构建通过，`/tmp/domainry-C04-ui-final-build.log`，保留原有大 chunk 提示。 |

网页逐项断言：

1. 首次确认包含两项实际参数、效果为 0；页面刷新和完整宿主重启后列表完全一致。
2. 用户批准两项后故意丢弃响应，再重启宿主；原 Run `crun_a6ae156ecdef840037a82ee28e5df935` 继续，两条待办各创建一次。新增第三项仍等待确认，用户拒绝后保留前两项。
3. 仅批准第一项时只创建一条；第二项继续等待，拒绝后没有第二条。
4. Identity 撤销工具和答复权限时批准返回 403，运行事件序号及效果不变；恢复权限不会自动提交，用户明确重试后 Run `crun_2c1abd19766ed75ae67b041d26f12227` 完成并且恰有两条待办。

已查看确认列表及第三项单独确认截图，内容可读；最终报告另含手机宽度完成页。初次浏览器脚本误匹配折叠的工具参数 `<pre>`，实际确认卡片已正确显示；将选择器限定到确认卡片后完整通过。初次恢复夹具使用默认 30 秒租约而只等待 5 秒，已将该测试的租约显式设为受支持的 300 毫秒；没有修改生产租约配置。

## 复现

```sh
go test -race ./integration ./internal/assembly/web ./internal/infrastructure/persistence/database/agent \
  -run '(ListedOperation|Interaction|Confirmation|Cancel)' -count=1 -timeout 3m
npm --prefix frontend run build
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web \
  -run '^TestListedOperationScopeThroughIdentityHTTPAndBrowser$' -count=1 -v -timeout 8m
# 上一命令提示 8092 就绪后，在另一终端运行：
AGENT_PLAYWRIGHT_MODULE=/absolute/path/to/playwright node frontend/tests/operation-scope.browser.mjs
```

Runtime 验收使用本地 SDK 临时工作区 `/tmp/domainry-C03-runtime.work`；在 Runtime 仓库执行 `GOWORK=/tmp/domainry-C03-runtime.work go test ./runtime/bootstrap/integrationtest -run '^TestConversationListedOperationsThroughRuntimeIdentityAndRestart$' -count=1 -v -timeout 5m`。依赖发布与升级仍按 H 项执行，本批未发布依赖、未修改现有部署或使用真实模型密钥。
