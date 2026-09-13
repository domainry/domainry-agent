# C05 独立结果读取：复现记录

所有 Go 命令从 `/Users/tiger/Projects/domainry-agent` 执行，使用当前 `go.work`。账号、OAuth、邮件服务及数据库均为隔离测试夹具；没有操作真实邮箱或用户数据。

| 日志 | 命令／范围 |
| --- | --- |
| `unit.log` | `go test ../domainry-tools/... ../domainry-tools-sdk/... ./internal/application -run 'TestIndependentResultRead\|TestDeliveryToolRead\|TestPeer.*Private\|TestCollaborationDeliverySource' -count=1` |
| `mail-http.log` | `go test ./internal/assembly/web -run '^TestPeerDeliveryReadsMailWithoutToolExecutionPermission$' -count=1 -v` |
| `full-regression.log` | `go test ./... ../domainry-agent-sdk/... ../domainry-tools/... ../domainry-tools-sdk/... -count=1` |
| `race-final.log` | `go test -race ./internal/application ./internal/assembly/web ../domainry-tools/internal/application/tool -run 'TestIndependentResultRead\|TestDeliveryToolRead\|TestPeerDeliveryReadsMailWithoutToolExecutionPermission\|TestPeerMessagesAndDeliveriesDoNotSharePrivateAttachmentsImplicitly' -count=1 -v` |
| `browser-http.log` | 下列环境加 `go test ./internal/assembly/web -run '^TestPeerCollaborationPermissionsRecheckCurrentRoleAcrossEntrypoints$' -count=1 -v` |
| `before-fixture-fix.log` | 首次邮件 HTTP 场景失败：撤销工具执行权限后，夹具尝试编辑已经不在目录中的工具偏好。将偏好验证移到撤权前；没有放宽产品权限。 |

Chrome 环境：

```sh
AGENT_PEER_PERMISSIONS_BROWSER=1
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright
AGENT_UI_TEST_OUTPUT=/tmp/c05-result-read-browser
```

全量回归之后的最终增量只补传原幂等键，以及“额外授予 execution_read 仍不能读取原工具结果”和“失败调用未开始”的断言；`race-final.log` 覆盖该最终增量。没有将较早编译的全量测试冒充最终补充断言的执行记录。本批未修改前端代码；Chrome 回归使用上一阶段构建的实际前端。

`permissions-report.json`、桌面和窄屏截图验证当前权限投影、运行／历史关闭、管理表单撤权及显式初始化。`source-sha256.json` 记录本批结束时的相关源码摘要，不改写上一阶段的历史证据。四个仓库均执行 `git diff --check`。

这份证据仅验收独立结果读取端口和已接入的邮件／日历／网页读取工具族；真实外部协议链路是邮件夹具。C05 仍有其他 owner 读取策略、跨用户协作和资料共享待办，完整清单仍为 4／25。
