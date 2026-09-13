# C04 转交验证命令与证据范围

命令在 `/Users/tiger/Projects/domainry-agent` 运行，前端命令在该仓库的 `frontend` 中运行。SDK 通过此目录的 `go.work` 使用相邻本地源码。测试模型、Identity 数据库、HTTP 外部服务与文件效果均为隔离夹具。

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent -run '^TestPeer(Transfer|StructuredInput|Dependency)' -count=1 -v
go test ./module ./internal/application ./internal/infrastructure/persistence/database/agent -count=1
go test ./internal/application ../domainry-agent-sdk/... ./internal/transport/http/module -count=1
go test ./internal/application -count=1
go test ./internal/assembly/web -run '^TestPeerTransferAgentToolContinuesRemainingHTTPWork$' -count=1 -v
go test ./internal/infrastructure/persistence/database/agent -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run '^TestPeer(Transfer|Outcome)' -count=1 -v
go test -race ./internal/assembly/web -run '^TestPeer(Transfer|Outcome)' -count=1 -v
```

`sdk-http-contract-run.log` 中 SDK 和 HTTP Module 已通过，但新增历史权限测试的 service fixture 漏设 runtimeID，先触发 principal_required。修正测试装配后，应用全套见 `application-final.log`。随后修复截图发现的旧错误码残留问题，持久化全套见 `store-final.log`。

`race-before-fixture-binding.log` 的应用、持久化、原回执 HTTP 及用户转交均通过，但 Agent 转交的重启夹具在 Open 返回后才绑定新宿主，worker 可能先读到已关闭的 Identity。夹具改用宿主现有 Prepare 钩子，在 worker 启动前绑定；三项 HTTP 场景重新通过 race，见 `transfer-race-final.log`。保留原始失败记录，不将部分通过伪装为全通过。

```sh
AGENT_PEER_TRANSFER_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/peer-c04-transfer-browser-verified \
go test ./internal/assembly/web -run '^TestPeerTransferOnlyContinuesRemainingHTTPWork$' -count=1 -v

npm test
npm run build
```

浏览器日志 `browser-http-final.log` 对应 `report.json` 与四张截图。截图来自真实 Chrome 的页面操作，覆盖表单、接手历史、原回执复用和窄屏；接手后的实际调用计数、外部效果及重启验证由相同 HTTP 场景断言。`frontend-tests.log` 为 66 项通过；构建日志包含既有 Vite chunk 大小提示。

`source-sha256.json` 记录本批相关源码和测试文件，路径相对 Agent 仓库。后续继续实现 TODO 时，以摘要区分本次验收源码与后来的修改。
