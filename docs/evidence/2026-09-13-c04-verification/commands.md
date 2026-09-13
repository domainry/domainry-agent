# C04 完成条件核对：命令与证据范围

Go 命令均从 `/Users/tiger/Projects/domainry-agent` 执行，SDK 通过当前 `go.work` 读取相邻本地源码。前端从该仓库 `frontend` 目录运行，使用 Node 22.22.0。

```sh
go test ./internal/application ./internal/execution ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./module ../domainry-agent-sdk/... -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run 'TestPeerVerification|TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery' -count=1 -v
npm test
npm run build

AGENT_PEER_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/peer-c04-verification-browser-final \
go test ./internal/assembly/web -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -v
```

`core-sdk-http-final.log` 对应核心、迁移、SDK 与 HTTP 契约全套通过；`frontend-tests.log` 为 68 项通过。`browser-http-final.log` 对应真实 Chrome 的 `report.json` 和三张核对截图，并在同一 HTTP 测试后半段验证宿主关闭重开、四条不可变历史和来源权限撤回。`race-final.log` 的应用、持久化和同一 HTTP 场景全部通过；HTTP 场景在 race 下约 78 秒。

本目录保留失败记录，不将部分通过记作整体通过：

- `before-source-projection-fix.log`：加入回执检查后，来源权限撤回曾让整个委派详情返回 403；已恢复原有元数据投影行为，并同时隐藏交付和核对详情。
- `before-route-count-update.log`：新增交付历史路由后，原契约测试的路由／Action 总数仍是旧值。精确目录及授权投影测试随路由更新并重新通过。
- `before-issuer-fixture-schema-fix.log`：HTTP 测试模型错误地提交只属于传输层的 `client_id`，被工具 Schema 正确拒绝；夹具改为按真实工具参数生成请求。未放宽生产 Schema。
- `before-restart-fixture-config-fix.log`：测试前半段故意修改了调用者配置以检查宿主隔离，重启时需恢复原配置；修正夹具并保护失败时的关闭回调。
- `before-race-fixture-wait-fix.log`：原通用测试等待仅 10 秒，race 下发起方的跨 Agent 来源核对未在该时限结束。该场景改为最多 90 秒并报告异常终态，未修改生产超时；最终实际完成与全部断言通过。

原工具账本没有因模型正文附加 `reference` 而改写。接收方夹具从消息正文获取引用，发起方从真实 `delegation_get` 获取当前交付摘要并执行 `review_delivery`。测试没有依赖模型内部引用字段来伪造端到端证据。

`source-sha256.json` 记录本批相关产品源码、契约和测试的摘要，用于后续演进时区分证据范围。最后一次浏览器通过后只调整了测试专用的等待时限；产品源码和浏览器脚本未因此变化。
