# C05：业务写入原回执独立阅读

日期：2026-09-13。接续[业务查询原回执](testing-2026-09-13-c05-business-result-reading.md)，本阶段接入 `invoke_action` 和 `workflow_start`。C05 仍有跨用户／角色执行主体与其余来源策略待完成，整体保持 **4／25**。

## 权限与来源边界

Runtime 在应用 owner 的权限清单中，为发布的 Action 注册 `action.receipt.<actionKey>.read`，为启用的 Workflow 注册 `workflow.receipt.<workflowKey>.read`。这些权限明确覆盖原提交参数与中立回执；注册不会给任何现有角色自动授予权限，也不授予业务执行权。原参数可能含不能从普通记录字段推导的内容，因此不能仅凭对象 read 权限释放。

Action owner 的 `ReadInvocationReceipt` 使用实际读取者的当前 Identity bundle，执行完整权限／数据范围／deny 评估。它只读原执行账本，核对工作区、原执行人、动作／目标、原输入指纹、回执状态与有效期，再核对目标及全部产生的记录引用的当前读取范围。超过页面展示上限的引用也要核对。返回类型只含状态、原调用 ID 和记录引用，不含原始 handler 输出、记录值、凭据或审计明细。读取拒绝 command run-as 身份替换，不取得执行租约、不消耗确认凭据、不重新执行。

Workflow owner 的 `ReadAgentWorkflowReceipt` 解析当前启用的定义，核对原输入、用户范围内的幂等键、启动账本、执行人、有效期及当前流程参与范围。当前 receipt-read 权限完整评估后，只返回原执行／流程 ID；原流程变量和节点输出不进入结果。Runtime 业务宿主还核对关联记录当前读取权限。`accepted` 只表示流程已经受理。

业务宿主通过可选 owner 端口接入已有 `business_result_read`，严格解码参数与回执、校验来源／用户范围和发布定义版本，对照原回执而不替换历史值，读取结束重新解析当前身份并核对有效策略。Runtime 写回执由原账本证明，不接受另加的 snapshot proof。未接入可选端口的自定义 Action／Workflow owner 仍返回精确 unsupported，由 Agent 保留原授权路径；明确拒绝不会回退放行。

Agent 沿用当前／历史交付的原回执入口与现有页面。普通工具调用、原运行详情、结果私有范围和工具设置检查保留。没有新增 SDK RPC 方法、生产前端或数据库迁移；业务 RPC SHA256 仍为 `e1c83b73a9ea7a2eedaf06efe3af087ba3b3ab34cd9151ef14730733d5bd4f06`。

## 验证

- Action owner：原执行人／工作区、输入指纹、有效期、目标读取权限、完整引用集合、deny 规则、缺少读取端口、run-as 及损坏回执均验证；第 101 条引用撤权也拒绝，执行／事务／assurance 次数保持零。
- Workflow owner：原账本、实际执行人、流程关联、当前参与范围、禁用定义、deny 规则、失效和未完成回执均验证；读取不产生 execution claim、insert 或 update，返回值无私有执行内容。
- Runtime 真实 Identity／Record／Workflow／SQLite／RPC：实际创建一个客户并启动一次流程；原执行角色未自动获得新回执权。切换到只有明确回执权及来源读取权、没有 Agent 工具／业务动作／流程启动权的角色后，两份原回执可读；变更原输入、版本、凭证、范围、额外内容或撤权均拒绝。恢复、重启保持原客户与原流程回执。
- Agent 真实 HTTP／Identity／SQLite／worker：两次写操作各经实际确认后完成，提交原回执作为交付依据。撤回全部 Agent 工具执行权、仅保留协作 view／delivery_read 后，当前与历史的准确回执和原参数仍可读；原运行详情拒绝。停用工具或撤回来源阅读权隐藏交付，恢复和重启保持原结果；只有原来的两次写操作。既有五类业务查询交付也回归通过。
- 相关 Runtime owner 包全量测试、授权注册／真实 RPC、Agent application 全量测试及两种真实 HTTP 场景通过；Action／Workflow／业务宿主／真实 RPC 及 Agent application／HTTP 的相应 race 测试通过。命令与原始结果见[证据](evidence/2026-09-13-c05-business-write-receipt-reading/commands.md)。没有新增浏览器视觉验收声明。

## 当前限制与发布核对

原始执行账本仍限定同一用户；没有通过替换 UserID 实现跨用户协作。记录删除或当前来源不再可读时，回执隐藏；本阶段没有增加 tombstone 阅读例外。工作流定义禁用／变更或原账本过期也不会以新执行恢复旧回执。

额外运行整个 `runtime/bootstrap/runtime` 包时，发布模块集检查发现工作区 Agent 契约 `dc5d01754dd3a20ce0468674520037b7ef63e0b14b45f2293244f0bbe0b98251` 与 `config/runtime-module-set.lock.json` 中 Agent v0.1.22／SDK v0.1.14 的契约 `b631a007518f154d70f1aad139e632be969e0e0203a5f5373a8267a1a700d6cf` 不同。该检查按 Runtime 的 `scripts/ci/contracts/runtime-composition-test-matrix.md` 属于发布版本门禁，要求已发布依赖；本次按当前工作区约束使用 `/tmp/runtime-work/go.work`。没有改写发布锁、发布依赖或声称发布门禁通过。本次授权装配与真实业务 RPC 的定向检查通过；该差异留待完整协作能力发布时联合更新版本与摘要。
