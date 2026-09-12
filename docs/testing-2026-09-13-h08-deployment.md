# H08 实际模型协议与部署模式验收（2026-09-13）

H08 已完成。当前实际启用配置使用 Verdent `gpt-5.6-sol` 和 Responses 协议；实际部署是单个 `cmd/domainry-agent-web` 进程，组合编译前端、Identity Module、Agent Module 和同一路径 SQLite。真实模型日常工作、Chrome 页面、实际构建产物、完整进程退出与重启均已通过。

## 实际启用配置

| 项目 | 验收值 |
| --- | --- |
| 模型 Provider | `gateway` |
| 模型协议 | `responses` |
| 模型服务源 | `https://api.verdent.ai` |
| 模型 | `gpt-5.6-sol` |
| Web 可执行程序 | `cmd/domainry-agent-web` |
| 服务组合 | 单进程编译前端 + Identity Module + Agent Module |
| 持久化 | 单个 SQLite 数据库 |

非敏感服务配置来自用户本地 `~/Library/Application Support/domainry-agent/web-services.json`，权限为 `0600`；凭据仍由独立 owner-only 文件持有，未写进仓库、日志或浏览器报告。对 82 个新增源码、文档和证据文件逐值比对本地凭据，命中为 0；Authorization Bearer 字面值和私钥标记也均为 0。实际模型配置快照见[模型部署配置](evidence/2026-09-13-h08-deployment/model-deployment-config.json)，部署条件探测见[部署审计](evidence/2026-09-13-h08-deployment/deployment-audit.json)，扫描结果见[凭据泄漏审计](evidence/2026-09-13-h08-deployment/credential-leak-audit.json)。

`llm-proxy` 没有作为模型服务接入。Agent 仅消费它已有的 `POST /tool/web_search` 和 `POST /tool/web_fetch_jina` Web 工具接口；`/Users/tiger/Projects/devops/llm-proxy` 与 `/Users/tiger/Projects/anti/llm-proxy` 两个工作区均保持 clean，本项未修改它们。

## 真实模型日常工作

最终非浏览器验收通过 8 个连续阶段，全部运行状态为 `completed`，累计 29 次真实模型调用和 27 次工具调用：

1. 从先前会话查找发布讨论并创建三项待办。
2. 调用可信时间，将第二项改到明确的下一个周五。
3. 保存周报格式偏好。
4. 从实际待办生成周报并核对事实。
5. 完整关闭并重开 Identity／Agent／SQLite 宿主。
6. 在新会话找回历史、记忆和原第二项并完成它。
7. 读取成果后修改第二节，生成版本 2。
8. 导出不可变的版本 1，并核对下载正文仍包含原始事实。

`TestLiveDailyWorkThroughIdentityHTTP` 用时 94.46 秒，包用时 94.895 秒。逐阶段运行账本位于[真实日常工作证据](evidence/2026-09-13-h08-deployment/live-daily/)，完整日志见[真实日常工作日志](evidence/2026-09-13-h08-deployment/live-daily.log)。独立 Provider 探针还验证了 Responses 流式输出、显式历史输入和结构化摘要，用时 4.81 秒，见[Provider 真实协议日志](evidence/2026-09-13-h08-deployment/provider-live.log)。

## 编译产品与实际可执行程序

第一组 Chrome 验收由真实模型日常工作宿主提供编译页面。8 个阶段累计 27 次模型调用和 25 次工具调用，随后浏览器完成 5 个产品步骤：显示实际模型；读取重启后保存的三项待办及第二项完成状态；读取周报记忆；打开成果版本 2、切换并下载版本 1；从浏览器再发起一次真实 Responses 请求并在刷新后核对同一组消息 ID。登录后的 JavaScript 错误和控制台错误均为 0。报告见[浏览器结果](evidence/2026-09-13-h08-deployment/browser/report.json)，截图见[刷新后的真实模型会话](evidence/2026-09-13-h08-deployment/browser/live-model-reloaded.png)、[持久待办](evidence/2026-09-13-h08-deployment/browser/persisted-todos.png)和[成果版本 1](evidence/2026-09-13-h08-deployment/browser/artifact-version-1.png)。

第二组验收先实际构建 `/tmp/domainry-agent-h08-web`，SHA-256 为 `7ea755ab28c2f4fe3d9d77145b10835a07f85c1ccac0546b29c14167f1e5e46b`，再以隔离的 SQLite 文件启动它。Chrome 完成首次登录和强制改密，从页面发送真实模型请求并保存会话 `conv_cc1f131f097b8d5e7a9926aa4ad328b5`。整个进程收到中断并退出后，以同一二进制和数据库重新启动；改后密码仍有效，页面读到原会话及完全相同的消息 ID `msg_348c55bc422a80a2d5e3ecd14d21ddef`、`msg_25cfef711cfde42a251b84c54d2e0691`。两阶段 JavaScript 和控制台错误均为 0。结构化报告见[启动阶段](evidence/2026-09-13-h08-deployment/web-command-browser/seed-report.json)和[重启阶段](evidence/2026-09-13-h08-deployment/web-command-browser/verify-report.json)。

## 发现并修正的问题

- 首次使用 GLM／Chat Completions 运行时，周末执行的“本周五”有两个合理解释，旧测试却只接受已经过去的 2026-09-11。验收提示现改为明确的“接下来的周五”及 ISO 日期，同时仍要求先调用 `time_now`。该改动只修正验收输入，没有把预期结果写进业务实现。
- 日期输入修正后，实际 `glm-5.3-flash-free` 仍在最后一步直接回复文本，没有调用 `artifact_export`，因此不能作为 H08 启用模型。失败运行完整保留在[GLM 导出失败日志](evidence/2026-09-13-h08-deployment/live-daily-glm-export-failure.log)。随后将实际启用配置改为已经通过业务验收的 `gpt-5.6-sol`／Responses，并从本地服务配置重新加载执行。
- 第一版浏览器脚本误以为 `/app/config` 会公开模型名；产品实际只在登录后页面展示模型。脚本已改为先从公开配置核对 workspace，再从登录后的 `.model-name` 核对模型。
- 浏览器功能步骤首次全部通过后，登录前预期的 401 和 favicon 404 被误计为业务控制台错误。最终报告单独保存这些登录前响应，并要求登录后的 JavaScript／控制台错误为 0。

这些中间失败均保留原日志和运行账本，没有用最终成功覆盖。

## 数据库和部署条件

H08 原文要求“启用 PostgreSQL / MySQL 或多实例前”补真实数据库与并发验收。当前实际命令明确只启动一个 `cmd/domainry-agent-web` 进程并使用一个 SQLite 路径；没有 PostgreSQL、MySQL、多实例或独立 SaaS 配置，当前 shell 也没有数据库连接配置，Docker daemon 未运行。因此这些条件尚未触发，本项不宣称已经验收未启用的数据库或多实例模式。将来启用任一模式前，必须先增加对应真实数据库、迁移、锁竞争、任务领取、幂等和多实例恢复验收。

Module／SaaS 装配契约仍以 race 连续三轮验证，但这只证明公开组合方式和默认解析没有回归，不冒充实际远端 SaaS 部署。

## 架构边界

模型 HTTP 协议转换和凭据属于 `internal/infrastructure/provider`；application 只依赖 Agent SDK 的模型和工具端口。`cmd/domainry-agent-web` 是组合根，负责把公开 Identity Module／SDK、Agent Module、SQLite 和 HTTP 页面绑定起来。Identity 继续独占 session、用户、角色和 workspace 事实；模型 Provider 不读取这些服务的持久化实现；前端只调用公开 HTTP 接口。

新增的两份浏览器脚本只从产品页面和公开 HTTP 接口观察行为。`internal/application` 的生产代码扫描没有 Identity、Integration、Runtime、Tools 或 Knowledge 的 `internal`／`module`／`saas` 实现导入；架构 race 连续三轮通过。机器可读结果见[架构审计](evidence/2026-09-13-h08-deployment/architecture-audit.json)。

## 回归结果

- `GOWORK=off go test ./...`：通过，Web 包用时 159.777 秒。
- `GOWORK=off go test -race ./internal/infrastructure/provider ./cmd/domainry-agent ./cmd/domainry-agent-web -count=3 -v`：通过；Provider 1.788 秒，命令包 18.200 秒。
- `GOWORK=off go test -race ./integration -run '^(TestConversationModuleSaaSAndBrowserSurface|TestConversationOnlyBindingResolvesLiveDefaultsAndPreservesExplicitOptions)$' -count=3 -v`：通过，11.376 秒。
- `npm test`：61 项通过；`npm run build`：通过，用时 7.62 秒。
- `GOWORK=off go vet ./...` 与 `GOWORK=off go build ./...`：通过。
- `GOWORK=off go test -race ./internal/architecture -count=3 -v`：通过，1.688 秒。

原始日志、JSON、下载文件和截图位于[H08 证据目录](evidence/2026-09-13-h08-deployment/)，完整机器清单见[结构化证据](evidence/2026-09-13-h08-deployment.json)。
