# C05 共享 Agent 配置与执行主体证据

Go 命令在 `/Users/tiger/Projects/domainry-agent`、现有 go.work 下执行。前端使用当前 Node 22 和已安装依赖，真实 Chrome 使用 `/tmp/domainry-g05-browser/node_modules/playwright`。没有新建分支、worktree 或提交。

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestSharedAgent\|TestAgentSharing\|TestAgentSnapshot' -count=1 -v` | [快照、隔离、全局名额与原子容量上限 PASS](initial-subject-tests.log) |
| `go test ./internal/assembly/web -run '^TestSharedAgentHTTP' -count=1 -v` | [实际双用户 HTTP、工具效果、撤权与重启 PASS](http-final.log) |
| `go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/capability ./internal/transport/http/module ./remote ./server ../domainry-agent-sdk/...` | [回归 PASS](regression.log) |
| `go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run 'TestSharedAgent\|TestAgentSharing\|TestAgentSnapshot' -count=1` | [PASS](race-http.log)，实际双用户 Web 60.612s |
| `go test ./internal/application -run '^TestAgentSharingDirectory\|^TestAgentSnapshot' -count=1` | [成员拒绝与查询故障 PASS](directory-policy.log) |
| `go test ./remote ./internal/infrastructure/persistence/database/agent -run 'TestSaaSAgentSharing\|TestSharedAgent\|TestAgentSharing' -count=1` | [SaaS 字段与私有终态历史 PASS](transport-and-history.log) |
| `go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./remote -run 'TestAgentSharing\|TestSharedAgent\|TestAgentSnapshot\|TestSaaSAgentSharing' -count=1` | [最后新增边界 race PASS](race-final-boundaries.log) |
| `go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote -count=1` | [最终三包 PASS](final-core.log) |
| `node --experimental-strip-types --test frontend/src/collaboration-state.test.ts` | [8 项 PASS](frontend-state.log)，含服务端只读字段和共享名单省略／清空语义 |
| `npm --prefix frontend run build` | [TypeScript 与生产构建 PASS](frontend-build.log)，保留现有大分块提示 |
| `AGENT_SHARING_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node AGENT_PLAYWRIGHT_MODULE=/tmp/domainry-g05-browser/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c05-agent-sharing-browser-final go test ./internal/assembly/web -run '^TestSharedAgentHTTP' -count=1 -v` | [真实双用户 Chrome PASS](browser-final.log)，[结构化报告](agent-sharing-report.json)、[桌面](shared-agent-recipient.png)、[390px 窄屏](shared-agent-mobile.png) |

28 份[源码摘要](source-sha256.json)保存本阶段完成时的源码快照。Agent 与 SDK `git diff --check` 通过。

## 测试夹具修正

- [HTTP 首次结果读取失败](http-result-page-fixture-failure.log)：测试请求 32768 字节，当前公开分页上限是 8192。改为合法上限，生产分页规则未改动。
- [浏览器首次刷新检查失败](browser-mobile-navigation-fixture-failure.log)：390px 下刷新后侧栏入口收起，测试直接查找桌面入口超时。窄屏共享、修改和撤回已完成；刷新检查改回桌面宽度再打开目录，最终全流程通过。

[阶段判定](acceptance.json)仅覆盖共享配置与当前调用者执行，C05 仍未整项完成。专业权限不会随配置共享转给其他用户，具体委派参与人和跨执行主体协作仍在原清单继续。
