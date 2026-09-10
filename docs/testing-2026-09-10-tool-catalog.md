# A03 工具目录、连接状态与工具开关验收

日期：2026-09-10。A03 已完成并勾选，下一项为 A08；本批不推进 K07，也不把 F01 的外部账号管理算作已实现。

## 实现

- SDK 新增可选 `ConversationToolAvailability`：宿主依据当前 runtime／workspace／user 与工具键返回是否启用、所需连接是否可用。明确关闭、断开、过期或未知均返回不可用；接口不传凭证、不执行刷新或工具效果。未绑定此可选策略的旧宿主继续通过原目录／具体动作授权负责可用性，系统不自动探测任意外部连接。
- [ConversationOptions](../internal/application/conversation_service.go) 支持显式绑定；直接工具宿主、[普通 Module 宿主](../module/factory.go) 及[延迟装配宿主](../module/conversation_assembly.go) 实现该接口时自动接入。显式配置优先，SDK 原必需接口不变。
- [executionCatalog](../internal/application/conversation_execution.go) 先取得已有挂载能力和 Identity 权限过滤后的目录，再校验注册契约、复制快照、排序和检查可用性。读写影响、权限动作、超时、结果上限和幂等策略均来自原注册，状态策略只能移除工具。
- 每个新模型步骤、冻结步骤恢复、实际工具调用前重新检查。目录中的连接状态检查共享 5 秒 Context，调用前的状态检查也有 5 秒上限；原宿主 Identity／目录解析保留调用者的执行时限，宿主须尊重取消。查状态的内部错误统一为 `tool_availability_failed`，不暴露账号或凭证文本。具体授权后才发生停用时返回 `tool_unavailable`，不会把未调用的工具记录为已发生外部效果。
- [历史来源检查](../internal/application/conversation_sources.go) 在重新访问业务／知识源前也检查连接状态；断开后隐藏相关历史回复，阻止为了重验旧内容而继续调用已停用连接。恢复后沿用当前权限复核。
- [网页错误说明](../frontend/src/errors.ts) 区分不可用工具、连接检查故障和模型错误，复用已有“继续处理／重新生成”入口。原始冻结步骤保持不变，已完成调用按原记录复用。

## 自动化测试证据

- [目录单测](../internal/application/conversation_tool_catalog_test.go)：完整注册元数据保留、按键排序、状态变化立即生效、宿主修改原缓冲区不影响冻结快照；关闭状态不能掩盖重复注册；错误脱敏及取消传播；连接检查的限时不缩短原有 Identity／目录解析时限。
- [执行集成](../integration/conversation_tool_catalog_integration_test.go)：完成写操作后模型中断，重启并关闭连接，冻结输入不再进入模型；恢复后复用原结果，实际写入 1 次、核查 0 次。另验证具体授权过程中停用连接，调用和核查均为 0 次。
- [Module 绑定集成](../integration/conversation_module_binding_integration_test.go)：延迟宿主绑定前不启动会话；绑定时继承宿主工具开关，关闭的 `calculate` 不进入目录，业务目录／真实存储流程继续可用。
- [Identity／HTTP 夹具](../internal/assembly/web/conversation_tool_catalog_test.go)：真实 Identity、模块装配、SQLite、Chat Completions HTTP／SSE 和官方知识 Connector；模型与知识上游为本地协议夹具。检查连接断开后的目录、未连接不调用上游、本地计算可用、Identity 撤权优先、连接错误及恢复、执行前停用、原步骤恢复，以及历史复核不能越过连接停用。

已通过命令和日志：

| 验证 | 命令 | 日志 |
| --- | --- | --- |
| Agent 全量 | `go test ./...` | `/tmp/domainry-A03-agent-full.log` |
| SDK 全量 | 在 `../domainry-agent-sdk` 执行 `go test ./...` | `/tmp/domainry-A03-sdk-full.log` |
| Agent／SDK 静态检查 | 各仓库执行 `go vet ./...` | `/tmp/domainry-A03-agent-vet.log`、`/tmp/domainry-A03-sdk-vet.log` |
| 执行与 Module 专项 race | `go test -race ./internal/application ./integration -run 'TestConversationCatalog\|TestDeferredConversationModuleRecoversOnlyAfterBusinessHostBinding\|TestConversationExecutionResumesAfterModelFailureWithoutRepeatingWrite' -count=1` | `/tmp/domainry-A03-race.log` |
| 限时范围修正后的专项 race | `go test -race ./internal/application ./integration -run 'TestConversationCatalog\|TestDeferredConversationModuleRecoversOnlyAfterBusinessHostBinding' -count=1` | `/tmp/domainry-A03-race-final.log` |
| 最终受影响包完整回归 | `go test -p 2 ./internal/application ./integration ./module` | `/tmp/domainry-A03-final-regression.log` |
| 前端既有 24 项状态回归 | `npm --prefix frontend test` | `/tmp/domainry-A03-ui-unit.log` |
| 前端类型与生产构建 | `npm --prefix frontend run build` | `/tmp/domainry-A03-ui-build-final.log` |

构建保留现有大分包提示；未把此提示当作构建失败，也未扩大本批到前端分包优化。

## 浏览器验收

使用[可重复脚本](../frontend/tests/tool-catalog.browser.mjs)，对临时 8092 `catalog-workspace` 验收宿主操作。独立无头 Chrome 使用新建测试 Profile；不访问桌面已有账号或浏览器 Cookie。

**最终通过 7 个网页场景，浏览器与宿主均以 0 退出，宿主测试 141.25 秒、race 通过，JavaScript 错误 0。** 报告中保存了 8 个不同的已提交 Run 及正式 assistant 消息 ID；两笔恢复均为原 Run 的第 2 次处理，其中恢复计算只保留一个完成调用。

1. 正常连接下，模型只收到已挂载且 Identity 授权的工具，未授权成果及未挂载业务工具不出现。
2. 知识连接断开并刷新后，知识工具从目录移除；本地计算仍完成并落库。
3. 关闭计算开关与撤销 Identity 计算权限分别生效；连接恢复不能覆盖权限撤销。
4. 模型选中计算工具后停用，实际调用被阻止；宿主关闭重开、网页刷新、恢复连接并继续后，同一个 Run 完成原调用。
5. 连接检查故障显示脱敏错误；恢复后通过“重新生成”完成同一 Run，内部错误中的测试凭证文字不出现。
6. 连接恢复后经实际 HTTP Connector 检索，正式回复保存；完整宿主重启后可读取。
7. 再次断开连接后历史回复隐藏，恢复连接并刷新后重现；HTTP 前置测试还核对断开期间历史复核没有访问知识上游。

最终证据：

- 脚本日志：`/tmp/domainry-A03-browser-6.log`。
- 宿主及 race 日志：`/tmp/domainry-A03-browser-host-6.log`，包含前置每轮状态／耗时；总计模型 HTTP 请求 20 次、知识 HTTP 请求 43 次，包含资料重验。
- 结构化报告：`/tmp/domainry-A03-browser-6/report.json`，包含场景列表、Run 状态观测、已提交 Run 与最终消息 ID。
- 截图：同目录 `ready-catalog.png`、`disabled-before-execution.png`、`restart-and-resume.png`、`connection-check-error.png`、`knowledge-restored.png`、`disconnected-history.png`。已检查恢复计算截图，运行显示“已保存”，不是仍在输出的临时文本。
- 启动命令：`AGENT_TOOL_UI_ACCEPTANCE=1 go test -race ./internal/assembly/web -run '^TestConversationCatalogIdentityHTTPConnectionsAndBrowser$' -count=1 -timeout 18m -v`；然后设置 `AGENT_PLAYWRIGHT_MODULE` 指向本机已安装 Playwright，运行 `node frontend/tests/tool-catalog.browser.mjs`。脚本首先校验 `catalog-workspace`，拒绝操作其他部署。
- 浏览器结束后自动关闭临时 Chrome，并调用测试宿主的 finish 控制；已确认 8092 无监听器。

上述模型和知识数据为本地协议夹具，身份、网页、HTTP、Connector、数据库、执行状态和重启是真实应用链路，不宣称这一批进行了新的真实 Verdent 模型验收。

## 修正与范围

1. 首轮具体授权后停用的专项测试发现稳定错误码没有进入 Run 的允许列表，显示成 `provider_failed`。已补 `tool_unavailable`／`tool_availability_failed`，随后专项及全量测试通过。
2. 首轮 Identity 测试把现有 resume 接口期望码误写为 202，实际契约为 200；仅修正测试断言，重跑通过。
3. 浏览器第 1 次在第 4 个场景因错误提示同时出现于消息和状态栏导致选择器不唯一；改为定位状态栏。第 2 次通过前 6 个场景，最后等待了后端占位文案，而 UI 按 `access_error` 显示来源不可用说明；截图确认原内容已隐藏，改为按实际 UI 状态断言。这两次不记作完整网页通过。
4. 第 3 次浏览器在计算终态等待 15 秒后超时，截图中计算调用已完成而运行仍显示处理中；没有将这一场景算通过。第 4 次宿主在前置断开检索场景的组合断言失败，未启动浏览器；原断言未输出具体原因，不能断言是上游被越权调用。随后补充每轮计时／状态日志及失败时的服务端快照，计时版完整 HTTP 流程通过（57.26 秒，`/tmp/domainry-A03-timing.log`）。
5. 复核发现新加的 5 秒 Context 原先包住了原有 Identity／目录解析；已收窄为仅限制新增连接检查，原授权沿用调用者时限，并新增回归测试。UI 和 HTTP 验收对实际终态保留最多 60 秒等待，仍严格检查完成状态、工具结果和调用次数。最终专项 race 见 `/tmp/domainry-A03-race-final.log`。
   第 5 次已通过 7 个 UI 场景，但截图中的最终回复仍可能处于流式写入；因此第 6 次增强为同时等待“已保存”、读取服务端 `completed` Run 与正式消息，再核对恢复前后的同一 Run ID。以第 6 次作为最终完成证据。
6. 当前未验收的跨库移动代码保留在工作目录，相关新增路由的数量断言已与 SDK 清单对齐；本批全量通过不代表 K07 场景已经验收，K07 继续未勾选。
7. F01 仍负责账号授权、刷新、撤销和管理页面，A03 负责消费宿主可用性并限制后续调用；已发出请求的取消边界仍由 B06 验收。真实远端账号生命周期、真实多库、部署／数据库组合仍由对应未完成条目验收；本批没有新调用真实 Verdent 或修改线上账号。
