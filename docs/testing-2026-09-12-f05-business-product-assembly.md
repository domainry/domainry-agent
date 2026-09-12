# F05 产品宿主配置与共享 Identity 装配增量

本阶段快照完成 Agent／Work／PM 共用的可选业务服务配置和共享 Identity 装配。后续默认产品业务选择和 Report 宿主增量见[架构边界](business-host-boundaries.md)，原阶段机器清单保持历史。F05 尚未完成：实际 Identity SaaS、Runtime 业务服务和产品会话／确认／网页按顺序继续验证；具体报表工具验收仍对应 N01–N03。

`webhost.Options.IdentityBinding` 借用公开 Identity Binding，经 Identity SDK 的 application wrapper 限定工作区与应用。宿主不注册应用、不重写原 Agent／Integration／Tools／Knowledge 权限来源，也不关闭借用的 Identity；本地 Knowledge 权限映射仍校验并保留。原宿主负责发布模块／产品权限，缺少权限不会被产品启动自动补权。本地 Identity、ExternalIdentity 与此借用模式有明确区别，借用和 ExternalIdentity 同配拒绝。

`Options.Agent.ConversationOptions.Business` 继续接受已有公共 Source；Web 宿主现在在延迟装配中提供该 Source。对具有 `businessrpc.Descriptor` 的服务客户端，宿主必须使用借用 Identity，且 Runtime、Workspace、Application、issuer、协议与来源身份匹配。Agent 应用层未导入 Runtime 或其业务实现。

Agent Web 与共用 `RunProduct` 入口已消费 `AGENT_BUSINESS_ENDPOINT`、`AGENT_BUSINESS_SERVICE_TOKEN`、`AGENT_BUSINESS_SOURCE_IDENTITY`、`AGENT_BUSINESS_CONTRACT_SHA256`；四项缺一即拒绝启动，完全未配置保持可选。Identity 通过已有 SDK remote factory 打开，业务客户端从真实 Identity descriptor 取得 issuer。`SAAS_APPLICATION_KEY` 允许 Work／PM 使用受管的既有 Identity 应用；远程模式不要求另一组本地 Identity 密钥。业务 token 只交给 SDK，未加入模型／浏览器配置。

验证结果：

- [共享 Identity 最终 race](evidence/2026-09-12-f05-business-product/shared-identity-final.log)：包 10.502 秒，实际 Identity／Integration／Tools／Agent 宿主同时存在。借用产品打开、关闭与错误 audience 启动均未访问 Identity 注册表、未关闭 owner，原权限快照仍可读取且哈希不变；本地映射保留。七种错误业务绑定分别拒绝。该专项不冒充真实业务服务的产品调用。
- [环境配置 race](evidence/2026-09-12-f05-business-product/environment-final-race.log)：包 1.572 秒，实际 SDK 进行了三次隔离 HTTP 握手。完整配置成功，Runtime／issuer 不同则拒绝；缺失配置及未绑定 Identity 均零 HTTP，未重试或泄露 token。Identity 非 origin／凭证 URL／非回环明文地址在请求前拒绝。既有 Integration 进程用例因未设置其 opt-in 环境而跳过，本日志不计为该进程验收。
- [Agent 回归](evidence/2026-09-12-f05-business-product/agent-regression.log)：Web 全量 113.114 秒，Product 装配、架构、profile 和 Web 入口通过；[Agent vet](evidence/2026-09-12-f05-business-product/agent-vet.log)通过。
- [Work 全量](evidence/2026-09-12-f05-business-product/work-regression.log)与[PM 全量](evidence/2026-09-12-f05-business-product/pm-regression.log)通过，含产品原 HTTP／默认配置测试；[Work vet](evidence/2026-09-12-f05-business-product/work-vet.log)和[PM vet](evidence/2026-09-12-f05-business-product/pm-vet.log)通过。

没有前端源码变化，本阶段未重建前端或重跑 Chrome。原本地产品回归不能证明远程业务的网页完整链路；该验收继续在 F05 内进行。源码和日志哈希见[机器证据](evidence/2026-09-12-f05-business-product-assembly.json)，已完成的真实 Runtime owner 测试见[前一增量](testing-2026-09-12-f05-runtime-business.md)。
