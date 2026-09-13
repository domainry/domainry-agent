# C05 业务动作／流程启动回执阅读证据

日期：2026-09-13。全部命令读取本地工作区源码。Agent 命令在 `/Users/tiger/Projects/domainry-agent` 执行，Runtime 命令在 `/Users/tiger/Projects/domainry-runtime` 执行且设置 `GOWORK=/tmp/runtime-work/go.work`。没有浏览器视觉验收、打包、发布或修改生产角色。

## 最终通过

| 命令 | 结果／日志 |
| --- | --- |
| Agent：`go test ./internal/application -count=1` | PASS，0.300s；[application 全量](agent-application-full.log) |
| Agent：`go test ./internal/assembly/web -run 'TestPeerBusiness.*DeliveryReads' -count=1 -v` | PASS，7.923s；[真实 HTTP](agent-http-final.log)；五类查询回执与两次经确认的写回执 |
| Agent：`go test -race ./internal/application ./internal/assembly/web -run 'TestDeliveryBusinessReads\|TestPeerBusiness.*DeliveryReads' -count=1` | PASS，Web 181.913s；[race](agent-race.log) |
| Runtime：`go test ./runtime/application/workflow ./runtime/application/action ./runtime/application/agenthost ./runtime/domain/action/projection ./runtime/domain/action/service -count=1` | 五个 owner／投影／注册包全量 PASS；[最终 owner 回归](runtime-owner-final.log) |
| Runtime：`go test ./runtime/bootstrap/runtime ./runtime/bootstrap/integrationtest -run 'TestRuntimeAuthorization\|TestBusinessWriteReceiptRead\|TestBusinessIndependentReceiptRead' -count=1` | PASS，真实 RPC 6.791s；[装配与 RPC](runtime-bootstrap-final.log) |
| Runtime：`go test -race ./runtime/application/action ./runtime/application/workflow ./runtime/application/agenthost ./runtime/bootstrap/integrationtest -run 'TestBusinessWriteReceiptRead\|TestActionIndependentReceiptRead\|TestWorkflowIndependentReceiptRead\|TestBusinessReceiptRead' -count=1` | PASS，RPC 34.740s；[race](runtime-race.log) |
| Runtime：`go test -race ./runtime/application/action ./runtime/bootstrap/integrationtest -run 'TestBusinessWriteReceiptRead\|TestActionIndependentReceiptRead' -count=1` | 加入原回执内部目标一致性检查后的最终 PASS，RPC 40.993s；[最终 race](runtime-race-final.log) |

Runtime 的 `GOWORK` 前缀适用于表内每个 Runtime 命令。Agent 与 Runtime 的 `git diff --check` 通过。27 个源码／发布锁依据的精确摘要见 [source-sha256.json](source-sha256.json)。旧阶段源码摘要保留旧快照，不随本次共用测试夹具调整而改写。

## 保留的中间结果与限制

- [首次 Agent 夹具失败](agent-initial-fixture-failure.log)：测试手写参数用了 `version`；实际 SDK 定义要求 `action_version`／`workflow_version`。工具正确拒绝，未执行。夹具改为直接序列化 SDK 请求结构后，真实确认、交付与 race 通过。
- [首次 RPC 夹具失败](runtime-rpc-initial-fixture-failure.log)：夹具把写 RPC 的 nil Go error 当成获准执行。已有 RPC 对拒绝和通信异常返回 `uncertain`；改用具体授权检查、原调用结果状态和实际记录／流程回执检查。生产 RPC 错误语义未更改。
- [首次 owner／RPC 通过](runtime-owner-rpc.log)保留详细的安全场景输出；最终文件另列，不覆盖原日志。
- [额外 bootstrap 全包回归](runtime-regression-published-pin-mismatch.log)中，仅发布模块集组合测试报告 Agent capability 摘要不匹配。命令为 `go test ./runtime/application/workflow ./runtime/application/action ./runtime/application/agenthost ./runtime/domain/action/projection ./runtime/domain/action/service ./runtime/bootstrap/runtime -count=1`。五个 owner 包通过；`runtime/bootstrap/runtime` 包未整体通过。
- 当前工作区 Agent capability 为 `dc5d01754dd3a20ce0468674520037b7ef63e0b14b45f2293244f0bbe0b98251`；发布锁是 Agent v0.1.22／SDK v0.1.14 的 `b631a007518f154d70f1aad139e632be969e0e0203a5f5373a8267a1a700d6cf`。Runtime 发布门禁说明要求固定已发布依赖，而本批工作区测试按约束使用 go.work。发布锁未更改，发布门禁不记为通过。

[阶段判定](acceptance.json)：本批独立回执阅读通过；C05 未整体完成，仍为 4／25。跨用户／角色执行主体、其余 owner 策略及联合发布版本核对继续保留。
