# 业务更新后的会话来源复核

日期：2026-09-10。此批修复 J04 的前置问题：业务记录正常更新后，旧读取结果与重新查询结果不同，原来的完整比较会隐藏历史回复并影响后续对话。**`invoke_action` 会话工具尚未交付，J04 不勾选。**

## 实现

- SDK `conversation_business.go` 增加可选 `ConversationBusinessEvidenceSealer` 与 `host_proof`。既有业务来源接口不增加必需方法。
- Agent `conversation_business_tools.go` 在保存结果前请求宿主证明，限制证明大小并拒绝签发失败；证明随原结果持久化，不改变冻结输入。结果恢复与历史读取继续调用宿主重授权。
- Runtime `conversation_business_evidence.go` 使用宿主提供的持久密钥签发 HMAC。证明绑定来源、所有者、参数、完整结果以及有效权限 / 对象 Schema 摘要。先完整重读核对内容，再签发；不为任意传入内容背书。
- 重新使用时验证签名、重新解析 Identity、检查当前查询条件，并复核所有已保存记录的当前读取权限。普通内容更新保留历史值；记录转移、删除、撤权或无法复核时隐藏。
- 有效策略改变时回到完整重读比较，不能凭旧证明放行。条件字段保持既有目录限制及记录策略；不扩大字段发现范围。
- Runtime 装配从 `IntegrationSecretKey` 派生专用密钥。没有密钥或历史结果未签名时保留原有语义；密钥轮换后旧证明失效。无需新表、新迁移或新数据库。
- 单条业务读取与关联起点读取增加已删除记录检查。

## 已通过的验证

Runtime `conversation_business_evidence_test.go` 使用实际记录服务、ORM 和 SQLite，Identity 主体 / 策略为夹具，覆盖：

- 查询后更新名称，记录离开原过滤结果，但历史值仍可使用且未被替换。
- 同样策略下将记录转给其他用户、删除记录，旧结果不可读取。
- 字段撤权和条件脱敏策略变化后隐藏；恢复等效策略后可再次访问。
- 改写数据、输入、来源、所有者、操作、权限摘要或签名均拒绝；不得签发虚构内容。
- 证明序列化后重建宿主仍可验证；更换密钥拒绝旧证明。
- 关联查询同时检查起点与已保存目标记录的访问权限。

Agent `conversation_business_evidence_integration_test.go` 使用实际会话执行器和 SQLite，模型 / 来源为夹具，覆盖证明持久化、模型中断、服务重建、相同冻结输入恢复、撤权隐藏，以及签发失败 / 证明超长不进入模型来源。

Runtime `conversation_business_snapshot_web_test.go` 使用实际 Identity、Runtime、Agent HTTP 和各自 SQLite，模型决策为夹具，完成：

1. 登录、分配角色、获取当前权限版本的令牌，并在会话中查询客户。
2. 调用普通 Runtime `customer.rename` 动作更新该客户，旧回答仍保留原内容。
3. 在原会话继续查询，新的回答取得更新后的名称。
4. 完整关闭并重开 Runtime 与 Identity，原浏览器会话仍可读取历史。
5. 切换字段受限及读取受限角色，历史隐藏；恢复角色后原历史恢复。

此测试没有通过模型调用 `invoke_action`，也没有进行本批真实浏览器点击或真实模型联调，不能作为 J04 的完整验收。

测试日志：

- Agent 全量：`/tmp/domainry-agent-snapshots-full.log`。
- SDK 全量：`/tmp/domainry-sdk-snapshots-full.log`。
- Agent 业务集成：`/tmp/domainry-agent-business-snapshots-2.log`。
- Runtime 业务应用测试：`/tmp/domainry-runtime-business-snapshots-4.log`。
- 实际 HTTP 修改、续聊、重启与撤权：`/tmp/domainry-runtime-business-snapshots-http-4.log`。
- 最终合并回归：`/tmp/domainry-runtime-business-snapshots-final.log`；签名与权限专项、原业务网页 HTTP、关联查询 HTTP、更新后续聊 / 重启均通过。应用包 0.524 秒，HTTP 集成包 59.876 秒。
- 相关 race：`/tmp/domainry-agent-business-snapshots-race.log`、`/tmp/domainry-runtime-business-snapshots-race.log`。
- 静态检查：`/tmp/domainry-agent-snapshots-vet.log`、`/tmp/domainry-runtime-snapshots-vet.log`。

调试中修正的测试设置：条件脱敏规则需要合法的 mask strategy；角色分配后原令牌的授权版本不适用于普通 Runtime 业务接口，需重新登录。没有放宽认证校验。

## 剩余范围

J04 仍需接入会话 `invoke_action`，验证具体业务目标 / 参数的确认、动作输入和记录版本、真实执行 / 幂等 / 未知结果核查、当前权限复核及网页交互。历史总数和游标仅描述当时的读取，多页结果不承诺同一事务快照。独立 Web 外部业务连接、依赖发布与部署验收也仍未完成。
