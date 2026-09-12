# E05：实际执行结果与具体步骤恢复

日期：2026-09-11。范围为执行结果展示、确定失败的修复入口、原运行续接和外部结果核查。模型和外部 HTTP 服务使用隔离夹具；Identity、Agent 服务、SQLite、网页及 Runtime 流程服务使用实际代码。本次不代表真实第三方业务 Connector 的故障验收，后者继续归 H06。

## 实现与架构

- `frontend/src/execution-outcome.ts` 根据已保存的调用状态统计完成、已受理、失败、待核查、未执行、中断及等待。`Run.status=completed` 表示回复已保存，不会盖过其中已确定失败的工具调用。页面明确计数对象为工具调用，业务完成情况以具体业务结果为准。
- `ExecutionOutcome.tsx` / `ExecutionActivity.tsx` 在对话和独立 `RunDialog` 中展示实际结果、错误原因和对应步骤的恢复入口。未完成的模型步骤即使没有工具调用也显示；失去来源读取权限时隐藏受限结果和恢复入口。历史窗口打开和刷新不会提交操作。
- SDK 的 `ConversationToolView.effect` 来自可信工具定义，`ConversationToolResult.completion` / 投影及历史执行条目的 `completion` 是可选的服务端结果字段。成功启动流程使用 `accepted`，与业务审批完成分开；拒绝读工具、失败结果或未知值伪造此标记。字段只扩展现有 JSON 快照、账本与事件，不新增迁移表。
- `conversation_execution_projection.go` 与前端事件归并在 `run.failed` 时保留成功、失败、已受理和未知回执，停止步骤标为 interrupted，未开始调用标为 not_started。快照与 SSE 事件仍在原事务内保存。已完成的确定失败回执也是历史事实，续接不会重新执行它。
- 对未完成运行，按钮继续调用现有 `/resume`；服务端从原冻结步骤恢复，重新授权并复用已保存回执。模型重试与外部核查分别沿用原步骤标识、工具幂等标识及宿主恢复契约。前端不会直接调用工具或跳过步骤。
- 对已有确定失败回执，按钮“准备修复这项失败”只填入新消息草稿，引用原会话、运行、步骤和调用。用户核对发送后才进入新运行；已保存结果不改写。该入口清除草稿中的个人记忆／待办／成果整类写授权，有现存草稿、待提交消息、活动运行或归档时不可覆盖。原运行结果不明时只提供核查入口。
- Runtime 继续通过公开 Agent SDK 的业务端口交付流程回执。本次 Runtime 仅增加实际流程验收断言，没有引入 Agent 与 Runtime 内部实现互相依赖；SDK 发布及消费者版本更新仍在 H 条目。

## 自动化证据

| 检查 | 结果与日志 |
| --- | --- |
| Agent 全量 `go test ./...` | 通过；`/tmp/domainry-E05-full.log` |
| Agent SDK 全量 `go test ./...` | 通过；`/tmp/domainry-E05-sdk.log` |
| Agent `go vet ./...`、架构边界及 diff 检查 | 通过；`/tmp/domainry-E05-vet.log`；架构测试在全量内 |
| 执行结果、确认、取消、组合授权和恢复相关 race | 通过；`/tmp/domainry-E05-race.log`：Store 1.788s、Web 72.001s、Integration 12.826s |
| 前端状态测试、类型检查及构建 | 30 项通过；`/tmp/domainry-E05-frontend-final.log`、`/tmp/domainry-E05-ui-final-build.log`；保留已有大 chunk 提示 |
| 实际 Identity / SQLite / HTTP 部分失败及模型恢复 | 通过；`/tmp/domainry-E05-http-fixed.log`，`TestExecutionOutcomeThroughIdentityHTTPAndBrowser` |
| 外部 HTTP 丢响应、原键核查及完整宿主重启 | 通过；`/tmp/domainry-E05-external-http-fixed.log`，`TestExecutionOutcomeExternalHTTPRecoveryAndBrowser` |
| 实际 Runtime 流程启动、待审批及最终审批结果 | 通过；`/tmp/domainry-E05-runtime-workflow.log`，`TestConversationBusinessWorkflowThroughIdentityWebAndRestart`；验证公开回执的 write / accepted，业务随后实际审批通过 |

`conversation_outcome_store_test.go` 验证已受理与确定失败的回执在失败、重新打开仓储及继续处理中保持一致，逐条事件重建结果等于持久快照；拒绝读工具或不合法状态使用 accepted。`execution-outcome.test.ts` 覆盖回复成功但工具失败、工作流受理、未知在途写、尚未开始调用、无工具的模型中断、撤权和已拒绝的确认。

实际 HTTP 测试创建两项待办，其中第二项时区无效：第一项真实保存，第二项保存确定失败回执；新修复运行先通过 `execution_read` 读取失败证据，再单独确认并创建第二项。另一场景在真实待办保存后模拟模型失败，完整关闭／重开宿主后继续原运行，待办 ID 和数量不变。

外部 HTTP 夹具将每项效果保存为临时文件，在第二项提交后直接关闭连接。第一次调用只得到结果未知；关闭／重开 Agent 和 Identity 后通过 GET 查询原幂等键的实际回执。夹具检查每个键只有一次 POST，GET 与原键相同，文件数等于获准操作数。它验证传输丢失与恢复协议，不冒充真实供应商业务。

## 网页验收

脚本：`frontend/tests/execution-outcome.browser.mjs`。使用本机 Chrome 的全新无头会话、编译后产品页面、8092 隔离实例及临时数据库。脚本先验证工作区为专用夹具，结束后关闭浏览器并停止临时宿主。

- 部分失败：网页明确展示“已完成 1 项、失败 1 项”，刷新后保持；实际待办数量为 1。
- 修复：从失败卡片准备草稿，核对原调用引用与整类写授权已清除；发送后执行历史读取、等待新确认，最后只新增缺失项。旧运行仍显示原失败，已有草稿时修复按钮禁用，刷新旧记录不覆盖草稿。
- 具体步骤继续：模型在真实写入后失败，完整宿主重启及页面刷新后，从历史窗口“从第 2 步继续”返回当前对话并恢复同一运行；实际待办 ID 不变、数量仍为 1。
- 外部核查：一项已完成、另一项结果未知，刷新和完整宿主重启后保持；未知项没有新修复请求入口，通过对应步骤的核查按钮取得确定回执，原运行与第一项资源 ID 保持。

本地网页 3 个场景通过，日志 `/tmp/domainry-E05-browser-local-verified.log`，实际宿主 `/tmp/domainry-E05-browser-local-host-verified.log`（29.571s）。外部核查网页通过，日志 `/tmp/domainry-E05-browser-external.log`，实际宿主 `/tmp/domainry-E05-browser-external-host.log`（33.527s）；包含 HTTP 预验收和网页在内共 4 个外部效果文件、4 次不同键的 POST、2 个原键核查，未重提写入。两个浏览器报告 JavaScript 错误均为 0，临时宿主均正常退出。

| 网页场景 | 原运行与实际结果 |
| --- | --- |
| 确定部分失败 | `crun_9485c1247f96772cc3713c887505a48f`，completed / attempt 2，但仅 1 项待办成功，另 1 项确定失败 |
| 新修复请求 | `crun_9e75c884620b308a683c000910273ff6`，completed / attempt 2，历史读取后重新确认，待办总数 2，第一项仅 1 份 |
| 后续模型失败后继续 | `crun_97e1f68b9d5e9b487428616d55c30737`，failed / attempt 2 → completed / attempt 3，待办始终只有原 ID 的 1 项 |
| 外部丢响应后核查 | `crun_337b2175f966a46c4cc8f0dba49fd48c`，needs_reconciliation / attempt 2 → completed / attempt 3；保留首项 `record-49f70c39a162f0ba`，核查获得次项 `record-622473d132d173ec` |

结构化证据：`/tmp/domainry-E05-browser-local/report.json`、`/tmp/domainry-E05-browser-external/report.json`。已目视检查本地目录 `resume-specific-step.png` 的部分结果与具体步骤按钮、`resumed-mobile.png` 的完成状态，以及外部目录 `external-unknown-mobile.png` 的未知结果提示与核查按钮。E05 已据上述代码和验收完成，下一项为 K01。

## 首次失败与修正

- 新仓储测试第一次重建 SSE 没有初始化原 attempt，触发已有严格校验；已按原运行初始化并验证事件与快照完全相同。HTTP 测试错误地把已有恢复接口的 200 写成 202，已修正测试。
- 外部夹具初次发现 Go HTTP Transport 会对带 `Idempotency-Key` 且请求体可重放的 POST 自动重试。夹具关闭请求体自动重放，以隔离并验证 Agent 层恢复；服务端仍以原键持久去重，未靠改键掩盖重试。
- 网页脚本先后漏了展开授权选项和关闭授权浮层，均停在不可点击元素处；补齐实际用户操作后继续。手机截图改为等待页面完成状态再拍摄，避免把后端已完成、SSE 尚未更新的瞬间当成最终显示。
