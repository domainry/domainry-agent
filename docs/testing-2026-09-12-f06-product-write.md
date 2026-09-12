# F06 产品装配、完整内容确认与外部写入验收

本批在此前 SDK、Google／Microsoft Provider、Integration owner、Tools／Agent 确认端口之上完成产品接入。本项产品、整段 HTTP 与实际浏览器验证已完成，F06 按单项交付勾选。完整文件哈希和检查结果见[机器清单](evidence/2026-09-12-f06-product-write.json)。

## 当前代码与职责

`internal/assembly/web/account_tools.go` 单独选装 `CalendarWriteTools`／`MailWriteTools`，沿用原读取工具。写入族共七项：发现日历写入账号、检查事件版本、创建／修改日程、发现邮件发送账号、发送／回复邮件。宿主仅消费公开 Tools 门面及 Integration SDK 的账号、读取、写入／回执端口；没有厂商 HTTP、直接读取 Integration 表或面向浏览器的通用写接口。

可信启动装配先从 Agent 原宿主取得确认校验端口，再组合已有产品工具；避免记录工具包装层遮蔽可选端口。账号适配器及可用性映射归本宿主实例，在 worker 启动前装配。Agent application、Tools 内部机制、Integration／Provider 实现及 Runtime 本批不作业务改动。

Integration SDK 声明 `integration.connection_accounts.write` 的权限元数据，不声明浏览器写路由。独立产品沿用自己的 Identity 权限发布和显式管理员设置入口；借用 Identity 的产品继续由原 owner 发布、授予权限和管理生命周期。用户提交确认仍需要既有的响应确认权限。权限声明不等于自动授权。

Agent Web 入口和 Work／PM 选装两个写入族；Work／PM 默认 Agent 更新到 1.5.0，原日历／邮件 Skill 更新到 1.1.0。Skill 明确当前账号及版本、完整 To／CC／BCC、单项／系列、清空语义、原邮件回复、精确批准和受理／送达未知。原业务 Action／Workflow 由 J04／J05／F05 的既有 owner 处理，未另建记录写入通道；Work／PM 原实际记录写入与三库装配继续回归。

写工具默认参数预算为 1 MiB，上下文预算为 2 MiB，以保留已确认完整参数和随后回执；显式部署预算保持原值，未选装的宿主保持原默认。曾用 60,089 字节邮件正文复现四个外部操作成功后 `execution_context_exceeded`；修正仅在产品选装边界，既不截断已确认正文，也不扩大通用执行引擎默认。

## 网页

新增 `AccountWriteOperationPreview`，由现有账号 SDK 查询当前可见账号并匹配冻结修订。未找到、已撤销或修订变化时不能批准。纯展示校验拒绝未知字段和不完整目标，业务语义及权限仍由后端 owner 验证。

确认卡显示账号、日历／事件、事件版本、单项／系列、带时区的确切时间或全天排他结束日期、参与者全表、明确清空内容，以及请求通知的含义。邮件显示所有 To／CC／BCC、原邮件 ID、主题和完整纯文本正文；长正文可滚动，字节内容不裁剪，HTML 字样只按文字显示。单项确认与冻结列表批准继续使用原交互服务。

结果卡仅按实际回执显示日程创建／修改及通知已请求；邮件显示服务已受理、送达尚未确认。没有实际邮件 ID 时不编造 ID。未知结果复用原 Run／执行身份查询 Integration 原回执，不重发。

## 已完成验证

| 验证 | 结果与证据 |
| --- | --- |
| 实际 Identity＋Agent＋Tools＋Integration＋公开 Google Provider＋OAuth HTTP＋SQLite | 60 KB 正文四场景通过，50.881 秒；整组确认前重启、重复确认、单项批准后拒绝、当前 Identity 撤权、断线后两次原回执核查。见 [最终 HTTP](evidence/2026-09-12-f06-product-write/product-http-final.log)。 |
| 精确内容与外部效果 | 整组创建／修改／发送／回复各一次；原生 PATCH 携带 If-Match，清空三字段而保留其余内容。实际 MIME 解码核对原邮件回复目标与所有收件人、完整正文。未知发送记为已受理但丢回执，重启后核查不产生厂商 HTTP。 |
| 选装／权限边界 | 三种写入族组合均要求持久确认端口，显式预算不覆盖，SDK 写权限未变成浏览器路由。借用真实 Identity 时不发布权限、不关闭 owner，最终 race 11.244 秒；见 [装配 race](evidence/2026-09-12-f06-product-write/composition-race-final.log)。 |
| Work／PM | 两产品 14 包测试／编译通过，原实际记录写入、默认 profile 与工具选择保持通过；见 [产品回归](evidence/2026-09-12-f06-product-write/work-pm-final.log)。 |
| Integration SDK | 四包完整 race 通过；见 [SDK](evidence/2026-09-12-f06-product-write/integration-sdk-race.log)。 |
| 前端状态 | 46 项通过，新增四项覆盖完整收件人／长正文、空值／未知字段、全天／系列／清空、DST 两种偏移保持原值；见 [状态测试](evidence/2026-09-12-f06-product-write/frontend-state-final.log)。 |
| 类型与三产品构建 | Agent 前端类型检查及 Agent／Work／PM 三套构建通过；见本目录 `frontend-check.log`、`frontend-build.log`、`work-build.log`、`pm-build.log`。构建有原有体积提示，无新增依赖。 |
| 静态检查 | Agent、Integration SDK、Work、PM 的 vet 通过；见 [最终 vet](evidence/2026-09-12-f06-product-write/vet-final.log)。 |
| Agent 全量 | 36 包测试／编译通过，其中 Integration 92.668 秒、Web 229.559 秒；含最终完整 MIME 收件人／正文断言。借用 Identity 与显式预算的后加断言另由上方最终装配 race 覆盖。见 [全量](evidence/2026-09-12-f06-product-write/agent-full.log)。 |

最终实际浏览器六场景通过，宿主 172.787 秒：35 次厂商 HTTP、21 次模型 HTTP、一次 OAuth 交换；整组四项、单项创建、未知发送合计六个外部效果，JavaScript 错误为零。60,089 字节正文逐字断言、当前原生 MIME 收件人／正文验证通过。见 [最终报告](evidence/2026-09-12-f06-product-write/browser-final/report.json)、[实际宿主核对](evidence/2026-09-12-f06-product-write/browser-final/host-audit.json)及 [日志](evidence/2026-09-12-f06-product-write/browser-final.log)。创建／修改日程、To／CC／BCC、正文末尾、实际受理回执和 390px 核查截图已逐张查看，无横向溢出；长卡片及正文通过各自滚动容器查看，截图不冒充一次显示全部长正文。第二轮亦通过，旧报告保留。

## 初次失败与修正

- 首轮测试会话 client ID 使用中文，不满足既有标识格式，已改为固定 ASCII；未修改生产约束。工具设置曾在并行构建和 race 下返回 503，夹具也曾循环重复读取全部目录，已改为一次读取。日志 `product-http-race-first.log` 保留。
- 15／60 秒轮询等待失败并非模型阻塞。剖析时四个 SSE 模型请求均约 0.2～1.3 ms 返回，主要开销为真实 SQLite／Identity 与 race 调度。夹具从 300 ms 租约改用生产默认 30 秒租约，并降低 idle worker／状态查询频率；没有修改生产权限或持久化实现。失败与 CPU／阻塞摘要分别在 `product-http-approved-second.log`、`product-http-approved-third.log`、`product-http-profile.log`、`profile-cpu.txt`、`profile-block.txt`。
- 完整确认前重启后曾返回 `interaction_access_denied`：夹具角色漏配既有响应确认权限，经 Identity 公开接口补齐；没有给生产用户自动授予权限。失败在 `product-http-approved-fourth.log`，修正后的短正文通过在 `product-http-approved-fifth.log`。
- 长正文曾在四项已成功后超出后续模型上下文，见 [原始失败](evidence/2026-09-12-f06-product-write/long-body-first.log)。当前通过产品预算修正并用完整正文回归，执行结果不被截断或重发。
- 浏览器首轮等待折叠的结果内容超时；脚本改为先点开“发送邮件”记录，再核对实际可见卡片，生产折叠行为未改。见 `browser-first.log` 和失败截图。
- 早先短正文的四场景组合 race 在最后一项达到 Go 默认十分钟总上限，退出 1；前三项效果／撤权断言均已完成，不能把该日志称为整包通过。见 `product-http-short-body-race.log`。最终剩余未知回执场景以 60,089 字节完整正文单独运行，race 通过（316.353 秒），包括实际受理后断线、完整重启、两次原 Run 核查、零额外厂商 I/O；见 [最终核查 race](evidence/2026-09-12-f06-product-write/unknown-receipt-race-final.log)。

## 验收范围

厂商与模型使用隔离 HTTP 协议服务；其余为实际产品代码、Identity、OAuth 流程、公开 Provider、Tools、Integration、Agent 与 SQLite。没有向真实收件人发信。Microsoft 原生五操作、Graph 202 无 ID、四种 owner 拓扑与并发／重启继续引用此前 [Provider](testing-2026-09-12-f06-microsoft-write.md) 和 [Integration](testing-2026-09-12-f06-integration-write.md) 的验证。本页的实际网页使用 Google Provider。

无真实厂商账号或 OAuth 应用配置，真实租户 CAS、邮件送达、真实模型质量、容量和整套部署不作为已通过结论；后续整体验收要求保留。llm-proxy 未改动，仍仅调用它已有的 Web 搜索／抓取两个接口。

复验使用仓库 `go.work`。HTTP：`go test ./internal/assembly/web -run TestAccountWritesThroughIdentityProviderHTTPAndRestart -count=1`；最终核查 race：同命令选择 `/unknown_original_receipt` 并增加 `-race -timeout 6m`。网页由 `TestAccountWritesBuiltBrowser` 启动实际宿主和 Chrome，需设置 `AGENT_ACCOUNT_WRITE_BROWSER=1`、`AGENT_NODE_BINARY`、`AGENT_PLAYWRIGHT_MODULE` 和独立的 `AGENT_UI_TEST_OUTPUT`；具体脚本见 [浏览器验收](../frontend/tests/account-write.browser.mjs)。未执行发布或更改 llm-proxy。
