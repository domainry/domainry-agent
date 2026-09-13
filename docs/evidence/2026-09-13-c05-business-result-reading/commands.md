# C05 业务原回执阅读：命令与证据

Agent／SDK 使用 `/Users/tiger/Projects/domainry-agent` 及既有 `go.work`。本阶段未创建分支／worktree、提交或部署。检查均已完成。

```sh
go test ./... ../domainry-agent-sdk/...
go test ./internal/application ../domainry-agent-sdk/...
go test ./internal/application -run 'TestDeliveryBusiness|TestDeliveryCollaboration|TestReleasedResult' -count=1
go test ./internal/application ./internal/assembly/web -run 'TestDeliveryBusiness|TestPeerBusinessDelivery' -count=1 -v
go test -race ./internal/application ../domainry-agent-sdk/businessrpc ./internal/assembly/web -run 'TestDeliveryBusiness|TestDeliveryCollaboration|TestBusinessResultReadRPC|TestPeerBusinessDelivery' -count=1
go test -race ./internal/assembly/web -run '^TestPeerBusinessDeliveryReadsWithoutProfessionalToolExecution$' -count=1 -v
```

`full-regression.log` Agent／SDK 全包通过，Web 289.097s，覆盖当前生产代码、Module／SaaS、架构与契约。`application-final.log` 为新增协作来源边界及业务来源变化检查；`http-final.log` 为撤回全部 Agent 工具权限后五类真实交付的通过记录（Web 5.881s）。

首轮合并竞态命令的应用与 SDK 通过；Web 已交付，因通知收尾超过夹具 60 秒等待而失败，见 `race-initial-timeout.log`。延长夹具的有界等待到 3 分钟后单独重跑，`web-race-final.log` 通过（98.117s），无数据竞态。生产代码没有因此调整；全包之后唯一代码变化是这个测试等待上限及失败诊断。初期 `http-initial-collaboration-denial.log` 暴露撤回协作工具权会阻止交付来源核对，已经通过具体来源策略修复，并验证直接读取原始协作记录不会继承该例外。

Runtime 均从 `/Users/tiger/Projects/domainry-runtime` 执行，使用独立既有工作区：

```sh
GOWORK=/tmp/runtime-work/go.work go test ./runtime/application/agenthost -run 'TestBusinessReceiptRead' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestBusinessIndependentReceiptReadThroughRealOwnerRPC$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/application/agenthost ./runtime/bootstrap/integrationtest -run 'TestConversationBusiness|TestBusinessReceiptRead|TestBusinessIndependentReceiptRead|TestReportAndAnalysisIndependentResultRead' -count=1
GOWORK=/tmp/runtime-work/go.work go test -race ./runtime/application/agenthost ./runtime/bootstrap/integrationtest -run 'TestBusinessReceiptRead|TestBusinessIndependentReceiptRead' -count=1
```

`runtime-owner.log`／`runtime-rpc.log` 覆盖真实数据、记录与字段权限和流程参与者；`runtime-regression.log` 通过（集成 144.699s），`runtime-race.log` 通过（集成 61.477s）。真实 RPC 角色变更和重启均保留原读取者及准确原回执。

SDK 的业务 RPC 契约 pin 为 `e1c83b73a9ea7a2eedaf06efe3af087ba3b3ab34cd9151ef14730733d5bd4f06`，新增一个可选方法；部署双方需共同更新，没有自动修改部署配置。Agent 普通 HTTP／SaaS 路由和 Action 数量未增加，无数据库迁移。生产前端未改动，未重复 npm／浏览器检查。

Agent／SDK／Runtime `git diff --check` 通过。`source-sha256.json` 保存三个仓库 HEAD 及 24 个代码／测试文件摘要。C05 仍未整项完成，整体 4／25。
