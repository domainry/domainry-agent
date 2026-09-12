# H09 截图场景端到端验收（2026-09-13）

H09 已按 TODO 原顺序完成七个场景。验收使用编译后的产品页面、Chrome、真实 Identity HTTP、Agent Module 和 SQLite；业务目录、关联记录与流程场景进一步组合实际 Runtime。测试模型只负责给出确定的工具调用，不把协议夹具冒充外部模型；实际启用模型与部署模式已经由 H08 单独验收。

## 按顺序执行的场景

| 顺序 | 场景 | 结果与直接证据 |
| --- | --- | --- |
| 1 | 计算精度 | 页面发起 `0.1+0.2`，实际 `calculate` 调用保存十进制字符串 `0.30` 和单位 `CNY`；完整宿主重开后同一运行仍显示 `0.1+0.2 = 0.30 CNY` 及舍入规则。浏览器 7 个连续步骤通过，JavaScript 错误为 0。见[浏览器报告](evidence/2026-09-13-h09-visual/calculation/report.json)和[重启后截图](evidence/2026-09-13-h09-visual/calculation/restart-and-resume.png)。 |
| 2 | 目录与关联记录权限 | 实际 Runtime 公开关系目录，页面从客户遍历两页订单、交付项目再返回客户；结果包含 `Order Alpha`、`Order Beta`、`Delivery Project` 和 `Acme`，不包含另一 owner 的 `PRIVATE-ORDER`。撤销关系字段后，刷新隐藏已保存回复和工具证据；恢复角色并完整重开 Runtime／Identity／Agent 后重新可见。见[报告](evidence/2026-09-13-h09-visual/relations/report.json)、[有权截图](evidence/2026-09-13-h09-visual/relations/relations-authorized.png)、[撤权截图](evidence/2026-09-13-h09-visual/relations/relations-field-revoked.png)和[重启恢复截图](evidence/2026-09-13-h09-visual/relations/relations-restored-after-host-restart.png)。 |
| 3 | 流程受理与完成 | 页面确认后，实际 Runtime 流程先显示已受理和等待；Runtime 审批后，只查询同一 process 的终态与 `approved` 结果，没有重复启动。随后拒绝另一笔确认，Agent 保存停止状态，Runtime 实例数不增加。见[报告](evidence/2026-09-13-h09-visual/workflows/report.json)、[等待截图](evidence/2026-09-13-h09-visual/workflows/workflow-accepted-waiting.png)和[完成截图](evidence/2026-09-13-h09-visual/workflows/workflow-completed-approved.png)。 |
| 4 | 全量分析与截断提示 | 长分析请求立即返回排队状态并在后台运行。Agent 账本只保存有界 preview 和稳定的完整结果引用；页面按引用重组 80 行，显示 `80 / 最多 100 行`、`完整：是`、`截断：否`、精度、缺失值说明和柱状图。撤销来源权限后，页面与直接完整结果请求都返回 403。见[报告](evidence/2026-09-13-h09-visual/analysis/report.json)、[完整结果截图](evidence/2026-09-13-h09-visual/analysis/analysis-full-result.png)和[撤权截图](evidence/2026-09-13-h09-visual/analysis/analysis-permission-revoked.png)。 |
| 5 | 附件私有范围 | 页面一次请求保存私有原件，上传不自动建立远端索引；用户显式执行后仅索引一个原件。Identity 撤权同时关闭页面能力并使直接请求返回 403，恢复后可继续。预览与下载字节和原件 SHA-256 完全一致，删除只发出一次远端清理请求；桌面和 390px 页面均显示“仅当前会话可见”。见[报告](evidence/2026-09-13-h09-visual/attachment/report.json)、[宿主审计](evidence/2026-09-13-h09-visual/attachment/host-audit.json)、[桌面截图](evidence/2026-09-13-h09-visual/attachment/desktop-private-index.png)和[手机截图](evidence/2026-09-13-h09-visual/attachment/mobile-private-index.png)。 |
| 6 | 成果版本与下载权限 | 页面明确选择版本 1，下载正文与所选版本字节一致。撤销导出权限后，读取仍可用，但按钮操作与直接导出均为 403；恢复后版本 2 下载正确，两个已保存版本未变化。再撤销读取权限，页面清空选中正文且直接读取返回 403。见[报告](evidence/2026-09-13-h09-visual/artifacts/report.json)、[版本 1 截图](evidence/2026-09-13-h09-visual/artifacts/artifact-version-1.png)、[下载撤权截图](evidence/2026-09-13-h09-visual/artifacts/artifact-download-revoked.png)和[读取撤权截图](evidence/2026-09-13-h09-visual/artifacts/artifact-read-revoked.png)。 |
| 7 | 关闭网页后的任务进度恢复 | 页面得到持久 task ID 和运行中状态后，整个浏览器 context 被关闭；没有页面连接时，服务端 worker 被释放并完成 `time_now`。新建浏览器 context、重新登录后读取到同一 task ID、2 个步骤、1 次工具调用和最终结果。见[报告](evidence/2026-09-13-h09-visual/task-page-close/report.json)、[宿主审计](evidence/2026-09-13-h09-visual/task-page-close/host-audit.json)、[关闭前截图](evidence/2026-09-13-h09-visual/task-page-close/task-running-before-page-close.png)和[重新打开后截图](evidence/2026-09-13-h09-visual/task-page-close/task-completed-after-page-reopen.png)。 |

计算报告记录的 JavaScript 错误为 0；关系、流程、分析、成果和关闭网页任务报告的 JavaScript 与最终控制台错误均为 0；附件报告的错误与外部请求均为 0。登录前预期的 401／favicon 404、权限验收预期的 403，以及关联场景完整宿主重开期间的暂态 503 都单独记录，没有混入最终页面错误。

附件原件与浏览器下载文件的 SHA-256 同为 `04b53692ecd93bab02e31b43499d98937d39d8c906726dce34b9de0fd1c36268`。成果版本 1 与版本 2 的 SHA-256 分别为 `2848c5eee95ba95df133bd86bbf00da332db65007b21c7b3a08f5cc09650429c` 和 `f3a81579c81e3ece871605df94d950246ec4982ee783a9f091f33b6b7cf9da11`，证明下载选择没有串版。

## 本轮修复

流程场景首次组合 Runtime 的指标中间件时，SSE handler 只检查最外层 `http.ResponseWriter` 是否直接实现 `http.Flusher`，因而把包装器后的可刷新连接误判成 `agent.conversation.stream_unavailable`。`conversationAdapter` 现在沿 Go HTTP 包装器的 `Unwrap() http.ResponseWriter` 契约寻找 `http.Flusher`，并用不认识 Runtime 实现的独立单元测试覆盖多层宿主包装。最终流程报告不再出现该 503，轮询回退不再掩盖 SSE 组合缺陷。

分析专项在 `-race` 下执行工具结果分页和成果写入比普通运行慢，原来的 5 秒启动观察窗和 30 秒完成观察窗会把仍在正常推进的任务误判失败。测试观察窗分别调整为 30 秒和 90 秒；产品预算、worker 超时和业务实现均未放宽。

## 架构边界

Agent application 继续只编排 Agent SDK 端口。Identity 独占当前主体、角色和 workspace 授权；Runtime 独占业务目录、记录、关系与流程；Knowledge／Connector 独占附件远端索引和清理；成果、分析结果引用和后台任务由 Agent 自己的公开服务边界管理。跨服务组合只出现在 Web composition 和验收装配中，没有 application 对其他服务 `internal`、`module` 或 `saas` 实现的直接导入。

SSE 修复位于 Agent 自己的 HTTP adapter，只依赖标准 `net/http` 能力发现契约，不导入 Runtime 指标 writer。机器可读边界检查见[架构审计](evidence/2026-09-13-h09-visual/architecture-audit.json)。

`llm-proxy` 仍只为 Web Search 和 Web Fetch 提供已有的 `POST /tool/web_search`、`POST /tool/web_fetch_jina`；模型调用不经过它，本轮也没有修改 `/Users/tiger/Projects/devops/llm-proxy` 或 `/Users/tiger/Projects/anti/llm-proxy`。

## 回归结果

- 七组 Chrome 端到端场景全部通过，场景日志、最终报告和截图位于[H09 证据目录](evidence/2026-09-13-h09-visual/)。
- `GOWORK=off go test ./...`：通过，Web 包用时 174.312 秒。
- H09 四个关键用例的专项 `-race`：全部通过；HTTP Module 包用时 1.681 秒，Web 包用时 188.847 秒，见[专项 race 日志](evidence/2026-09-13-h09-visual/validation/h09-race.log)。
- 分析长任务单独 `-race`：通过，用例 46.95 秒，包 48.774 秒。
- `GOWORK=off go test -race ./internal/architecture -count=3 -v`：六项边界门禁连续三轮通过，包用时 7.197 秒。
- `npm test`：61 项通过；`npm run build`：通过，用时 20.58 秒。
- `GOWORK=off go vet ./...` 与 `GOWORK=off go build ./...`：通过。

完整机器清单见[结构化证据](evidence/2026-09-13-h09-visual.json)，凭据扫描见[凭据泄漏审计](evidence/2026-09-13-h09-visual/credential-leak-audit.json)。
