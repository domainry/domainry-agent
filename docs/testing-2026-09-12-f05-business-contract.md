# F05 业务宿主 SDK 服务契约增量

这是最初 SDK 阶段快照。后续实际 Runtime 接入补充必填 Identity issuer，Report 宿主增量再次扩展契约；当前契约身份见[架构边界](business-host-boundaries.md)，实际 owner 的首轮验证见[后续增量](testing-2026-09-12-f05-runtime-business.md)。下文原哈希及机器清单保留历史，不代表当前兼容身份。

已先核对当前 Agent SDK 端口、Runtime 真实 `ConversationBusinessHost` 和独立 Web 宿主缺口，再增加独立 `businessrpc` 子包。详细归属和后续顺序见[业务宿主边界](business-host-boundaries.md)。本阶段只完成公开服务契约与隔离 HTTP 验证，F05 尚不勾选，下一步连接 Runtime 实际业务 owner。

协议 `domainry-agent-business-host-v1` 的 SHA256 为 `9037f423a375d37b5a73a1eca5b8ea92838d8510c5c3a2add3a5aa9f2c0f7b27`。哈希覆盖固定路径、限额、委托／不重试语义、请求／响应外壳及全部现有 DTO JSON 形状；公共 DTO 变化不会静默保留兼容身份。启动握手核对 Runtime、Workspace、Identity Application、source identity 和精确契约，每次调用再次核对绑定。

公开 Backend 组合现有 Source、Relation、Action、Workflow、EvidenceSealer、ToolAuthorizer 和 ExecutionAuthorizer。服务 adapter 不读记录表、不保存确认或回执，逐次调用后端当前主体与工具授权。具体行／字段、动作参数与版本、精确确认、幂等、流程和旧来源完整性仍属于实际业务 owner；服务 token 是明确固定 scope 的应用间委托凭证，不代替用户授权，两侧必须使用同一受信任 Identity 身份域。

16 个服务方法覆盖目录、查询、单条、关系、读取证据封印／复核、Action 授权／执行／核查／回执复核、Workflow 授权／启动／核查／状态／回执复核及工具授权。只接受有界类型化 JSON，不提供任意 SQL 或报表伪执行。固定两个内部服务路径，不接浏览器 Cookie；服务令牌不进入 DTO、模型或来源证据。请求与响应各最多 1 MiB、调用最多 65 秒，限制 HTTPS 或显式回环 HTTP、无内嵌凭证／查询／fragment／任意路径／非法端口，不跟随重定向且不提供自动重放的请求体。

[针对性最终 race](evidence/2026-09-12-f05-business-contract/sdk-final-race.log)通过，1.742 秒，包含：

- 16 个实际 HTTP 方法、每次主体与工具授权调用；`9007199254740993` 原 JSON 数字保持精度，Workflow accepted／waiting 没有被转成已完成。
- 当前工具权限撤销、主体停用、无服务凭证、跨工作区、source 变化、未知操作、嵌套身份不一致和注入 SQL 字段均在业务调用前拒绝；后端私有错误被脱敏。
- Context 取消到达服务端执行方法；写 owner 已产生效果但 HTTP 连接断开时，只出现一次写调用，客户端返回 uncertain；显式原键 reconcile 取得原回执，没有重放 invoke。
- 请求／响应限额、凭证重定向拒绝、错误 endpoint／typed nil 后端和已取消请求、公开契约固定哈希检查。

[SDK 全量 race](evidence/2026-09-12-f05-business-contract/sdk-full-race.log)通过，三个有测试包：根 1.482 秒、businessrpc 1.873 秒、persistence 2.179 秒；另外六包编译通过。公开外部 package 编译证明 Client 可赋给已有六个 SDK 端口，不需实现或 internal import。[全量 vet](evidence/2026-09-12-f05-business-contract/sdk-vet.log)通过。

[首轮失败](evidence/2026-09-12-f05-business-contract/sdk-initial-race.log)是夹具把实际 `ConversationBusinessObject.Label` 写成不存在的 Name；已按当前 DTO 修正，没有改变共享业务 DTO。[第二轮通过](evidence/2026-09-12-f05-business-contract/sdk-second-race.log)后补了配置边界与契约固定校验，再执行最终专项和全量。

[机器证据](evidence/2026-09-12-f05-business-contract.json)记录源码、原始日志和检查哈希。此阶段后端是隔离协议实现，尚未以这些测试证明实际 Identity／Runtime 行字段权限、独立产品或网页；它们按 F05 后续顺序验证。报表 owner 的实际查询仍按 N01 接入。
