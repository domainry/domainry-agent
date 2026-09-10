# 业务流程启动与进度验收

更新日期：2026-09-10。对应 TODO J05。**实际 HTTP、真实模型、网页整段验收及专项检查通过，J05 已勾选。**

## 实现位置

- SDK：相邻 `domainry-agent-sdk/conversation_business_workflows.go` 定义可选宿主契约、启动参数、受理回执和流程状态。
- Agent：[流程工具入口](../internal/application/conversation_business_workflows.go) 复用当前授权、持久确认、执行账本及未知结果核查；[专项测试](../integration/conversation_business_workflows_integration_test.go) 覆盖重启、并发重复确认和核查。
- Runtime：相邻 `runtime/application/agenthost/conversation_business_workflows.go` 接实际 Workflow；`runtime/application/workflow/workflow_agent_invocation_application_service.go` 提供按用户限定的持久启动和只读回执核查。复用既有流程 / 执行 / 回执表，不新增流程状态机或迁移账本。
- 网页：[流程确认预览](../frontend/src/WorkflowOperationPreview.tsx) 展示冻结输入；[流程结果](../frontend/src/WorkflowResult.tsx) 区分启动已受理、处理中和实际业务结果。

流程状态由 Runtime 维护。启动需用户具体确认，确认后继续检查权限和执行版本；审批通过操作使用 Runtime 原有业务审批接口，Agent 的进度查询不会审批或重新启动流程。历史查询保留当时状态，同时按当前权限复核。

## 专项与实际 HTTP

| 验证 | 证据与范围 |
| --- | --- |
| Agent 流程集成 | `/tmp/domainry-agent-workflows-new-2.log`；确认前服务重建、并发重复确认、未知启动结果核查、进度读取与撤权。模型 / 流程宿主为夹具。 |
| Runtime 宿主、Workflow 和持久回执 | `/tmp/domainry-runtime-workflows-regression.log`；三个相关包全量通过。验证当前权限、输入契约、签名历史状态及禁止重新领取过期的未完成启动。 |
| 当前目录与 HTTP 回归 | `/tmp/domainry-runtime-workflows-current-1.log`、`/tmp/domainry-runtime-workflow-catalog-regression.log`；实际 Identity / Runtime / Agent HTTP、SQLite 验证发起、等待审批、实际审批、结果查询、重复确认、重启和权限撤销 / 恢复。模型为夹具。 |
| 并发与静态检查 | `/tmp/domainry-agent-workflows-race.log`、`/tmp/domainry-runtime-workflows-race.log` 均通过；相关 Agent / Runtime 包 `go vet` 通过，日志为对应 `*-workflows-vet.log`。这是相关包和名称筛选检查，不代表 Runtime 全库测试。 |
| 前端 | `/tmp/domainry-agent-workflow-result-frontend.log` 构建通过；`/tmp/domainry-agent-workflow-result-check.log` 直接检查实际导出函数，受理、等待、通过、失败、拒绝、取消及无效 / 截断内容显示符合结果。构建仍有 bundle 大小提示。 |

额外修正：流程目录在 Schema 与当前执行定义不一致时，统一使用当前定义的名称、对象关系和版本；执行目标不可用时不再回退展示旧输入契约。专项测试覆盖旧 Schema 与新定义的对象过滤、版本变化和不可用目标。

## 真实模型和网页

使用 Verdent `gpt-5.6-sol` / Responses，业务数据与数据库为临时合成数据，模型、Identity 和 Runtime 服务均为实际调用。

首次真实模型测试 `/tmp/domainry-runtime-workflow-live-1.log` 在文字断言处失败。实际结构化状态已经是结束 / `approved`，模型回答“已完成、已批准”；测试只接受“通过”或 `approved`。修正为同时接受“批准”，保留对实际流程 ID、状态、业务结果以及唯一实例数量的校验。

本次 `/tmp/domainry-runtime-workflow-live-2.log` 已完成实际 HTTP 的初始流程：先取得当前输入契约，确认前关闭并重建宿主，重复确认后只产生一笔流程，查询到等待经理审批；经 Runtime 原有接口审批后查询到完成 / `approved`。完整重启后旧等待回复与新完成回复保持原文；撤权隐藏、恢复后重新可读。随后进入同一真实模型的网页验收。

网页已验证具体参数确认卡、确认前刷新 / 宿主重启、确认后实际受理、等待经理审批，以及实际审批后再查询得到已完成 / 审批通过。启动调用在页面显示“已受理”，查询调用显示“查询完成”；展开后显示实际流程状态与步骤。第三笔启动在系统确认卡被拒绝，页面显示处理已停止；撤权后历史流程内容隐藏，恢复并完整重启后，原来的等待、完成和拒绝记录均恢复。

测试收尾再次读取实际 Runtime 流程和 Agent 持久运行：只有两笔获准实例，均已审批通过且有完成时间；网页查询过第二笔实例的等待与完成状态，拒绝记录已保存，没有第三笔实例。`TestLiveConversationBusinessWorkflow` **通过，604.86 秒**，测试进程退出码为 0，日志 `/tmp/domainry-runtime-workflow-live-2.log`。临时网页和监听器已关闭。

本次包含 7 次处理（6 次完成、1 次因拒绝取消），19 次工具调用记录中 18 次完成、1 次启动被拒绝。两笔实际实例为 `process_1789031914311696000_1` 和 `process_1789032148780934000_12`。公开消息和运行证据保存在 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-business-agent-q2qtau8y/business-workflows-browser.json`；原始两阶段运行另存为同目录 `business-workflows-live.json`。文件仅含合成资料与公开运行投影。

本次模型在网页后续发起时先用文字征求确认，再进入系统确认卡；实际写入始终受系统确认控制。该额外交互仍有改进空间，不能把这次通过理解为所有模型在所有输入下都会只询问一次。

## 复现

在 Agent 目录运行，脚本使用相邻本地模块组成临时 go.work，并使用临时合成数据库：

```sh
python3 scripts/test-agent-business.py --workflows
python3 scripts/test-agent-business.py --workflows --live --browser --model gpt-5.6-sol --protocol responses
```

浏览器模式需先构建前端。真实模型配置沿用本地服务配置；凭证通过已有凭证加载方式或隐藏终端输入提供，不写入验收文件。网页完成流程确认后，测试专用 `approve-workflow` 控制入口调用实际 Runtime 审批接口；测试结束时再次检查实例数量、业务结果以及 Agent 保存的等待 / 完成查询与拒绝记录。

J05 不包含业务审批管理页面、Workflow 设计器、独立 Web 的远程业务连接或 C04 持久业务操作授权范围。它们沿用已有 Runtime 产品或保留在各自 TODO 中。
