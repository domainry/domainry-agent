# C05 报表与账号回执：复现记录

本批全量 Go 回归及相关 race 检查通过，C05 整体仍未完成，清单保持 **4／25**。

默认工作目录为 `/Users/tiger/Projects/domainry-agent`，使用现有本地 `go.work`。Runtime 命令从 `/Users/tiger/Projects/domainry-runtime` 执行，设置 `GOWORK=/tmp/runtime-work/go.work`。没有创建 worktree、分支或新数据库迁移。测试只使用临时数据库、模型／Provider 协议夹具和隔离的邮件地址，没有操作真实邮箱。

## 最终验证

| 日志 | 命令／范围 | 结果 |
| --- | --- | --- |
| `full-regression.log` | `go test ./... ../domainry-agent-sdk/... ../domainry-tools/... ../domainry-tools-sdk/... ../domainry-report/... ../domainry-report-sdk/... ../domainry-integration-sdk/...` | 通过；Agent Web 包 273 秒，其余包按 Go 的依赖校验复用或执行 |
| `runtime-regression.log` | Runtime：`go test ./runtime/modulehost/report ./runtime/application/agenthost ./runtime/bootstrap/transport ./runtime/bootstrap/integrationtest -run 'TestReport\|TestConversationGovernedReport\|TestConversationAnalysis\|TestConversationReport\|TestConversation.*RPC'` | 通过；应用宿主／transport 无匹配测试，仅记为编译；integrationtest 183 秒 |
| `report-source-rpc.log` | Runtime：`go test ./runtime/bootstrap/integrationtest -run '^TestReportAndAnalysisIndependentResultReadThroughRealOwnerRPC$' -count=1 -v` | 真实 Report／Identity／源数据／Tools／RPC；包括真实数据修改与字段／对象权限撤回 |
| `report-projection.log` | Runtime scope 的隐藏行／关系谓词测试，以及 Tools 偏好不依赖执行目录测试 | 通过 |
| `report-source-policy.log` | Report：`TestReportIndependentReadUsesSourceScopeAndPreservesExecutionAuthorization`、`TestAnalysisIndependentReadRechecksDataAndOriginalSpecification` | 通过 |
| `report-compatibility.log` | `go test ./internal/application ../domainry-tools/internal/adapter/reporttools ../domainry-tools/internal/adapter/analysistools -run 'TestDeliveryToolRead\|TestReportLegacyResults\|TestAnalysisLegacyResults' -count=1 -v` | 旧成果完整执行授权回退；非空凭证明确拒绝不降级 |
| `write-unit.log` | `go test ../domainry-tools/module -run '^TestAccountWriteResultRead' -count=1 -v` | 四种写入操作的原回执核对与拒绝边界 |
| `report-http.log` | `go test ./internal/assembly/web ../domainry-agent-sdk/businessrpc -run '^TestPeerReportDeliveryUsesReadingPolicyWithoutExecutableCatalog$\|^TestContractAndConfigurationFailBeforeIO$' -count=1 -v` | Report／Analysis 交付装配与已审查契约 pin |
| `write-http.log` | `go test ./internal/assembly/web -run '^TestPeerWriteDeliveryReadsOriginalMailReceiptWithoutSendPermission$' -count=1 -v` | 实际正常 worker、确认、OAuth／Provider 适配器及 owner 账本；邮件发送效果恰好一次 |
| `owner-write-regression.log` | `go test ../domainry-integration/... -run 'TestAccountWrite'` | 有匹配测试的 owner 应用、持久账本、架构、HTTP 与场景通过；其他包只编译 |
| `owner-write-exact-read.log` | `go test ../domainry-integration/internal/assembly/saas -run 'TestAccountWrite' -count=1` | 追加的真实账本只读场景通过：改原内容、未执行请求、另一主体均不能取得原成功回执 |
| `race.log` | `go test -race ./internal/application ../domainry-tools/internal/application/preferences ../domainry-tools/internal/adapter/reporttools ../domainry-tools/internal/adapter/analysistools ../domainry-tools/module ../domainry-report/internal/application/report ../domainry-agent-sdk/businessrpc -run 'TestDeliveryToolRead\|TestResultReadAvailability\|TestReportLegacyResults\|TestAnalysisLegacyResults\|TestAccountWriteResultRead\|TestReportIndependentRead\|TestAnalysisIndependentRead\|TestContractAndConfiguration' -count=1` | 通过 |
| `delivery-http-race.log` | `go test -race ./internal/assembly/web -run '^TestPeerReportDeliveryUsesReadingPolicyWithoutExecutableCatalog$\|^TestPeerWriteDeliveryReadsOriginalMailReceiptWithoutSendPermission$\|^TestPeerDeliveryReadsMailWithoutToolExecutionPermission$' -count=1 -v` | 三个场景全部通过，约 317 秒 |
| `analysis-table-read-race.log` | `go test -race ../domainry-report/internal/application/report -run '^TestAnalysisTableIndependentReadRequiresCurrentWholeSource$' -count=1 -v` | 全量命令启动后补充的表格分析阅读测试单独通过，未重开分析流 |

本批没有修改前端展示代码；HTTP 测试验证实际网页入口和持久 worker，不宣称新增了浏览器视觉验收或模型能力评测。来源数据版本核对可能读取 owner 当前数据以计算指纹；“不重新执行”指不再次运行原报表／分析或重放写入。邮件读取／回执场景额外断言无新增 Provider HTTP。

## 保留的失败与修正

- `report-owner-before-binding-fix.log`：新增 Report 非 HTTP 权限未声明 NonHTTP binding，manifest 校验失败。补 SDK invocation binding 后 owner 模块通过，见 `report-owner.log`。
- `full-before-contract-pin.log`：严格 RPC 契约测试检测到新增四个方法与 ReadProof 字段。核对公开 DTO／边界后更新预期指纹；没有放宽严格解码或握手。
- `report-http-before-routes.log`、`report-http-before-status.log`：新测试最初漏装工具设置 routes，随后把明确来源拒绝的交付历史响应误写为 200。修正测试装配和预期拒绝码；未放宽产品权限。
- `write-unit-before-import-fix.log`：测试文件未用 import，已移除。
- `write-http-before-grant-fix.log`、`full-before-write-fixture-fix.log`：个人工具 helper 已授予确认权限，重复追加被 Identity 拒绝；删除重复测试配置。
- `write-http-before-fixture-fix.log`：复用的邮件 Provider 夹具明确核对原收件人分组和全文，新测试需使用该夹具约定；另将账号阅读可用性拒绝正确记录为 503。均修正测试输入／预期，不修改邮件发送授权。

## 代码与兼容性记录

`source-sha256.json` 记录验收结束时相关 Go 源码、测试及各仓库 HEAD。部分前序 Report／RPC 实现已由外部操作提交，不能只用本轮 git diff 代表本阶段代码；本任务没有执行提交、推送或发布。相关仓库已执行 `git diff --check`。

RPC 当前契约：`fd3dbd5fd33572296c29d12d13545baffe458a835c0528c72b71f62eee86391b`。客户端／服务端及部署 pin 必须一致；当前角色须明确取得 `report.results.read`，没有自动给现有角色补授权。账号回执仅把已授权的阅读范围交给 owner 的只读账本入口，执行／恢复继续解析原写入权限。

本证据不覆盖尚未实现的跨用户委派、Knowledge／业务的全部来源策略或显式共享页面。私有附件没有因这些阅读端口被自动共享。

