# K07 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-16。

```sh
cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./server ./internal/transport/http/module -count=1
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/infrastructure/persistence/database/agent -run '^TestExternalAgent' -count=1 -v
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/assembly/web -run '^TestExternalPeerAgentProtocolUsesExactIdentityAndShowsReportedExecution$' -count=1 -v
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./remote -run '^TestSaaSExternalAgentProtocolPreservesExactAuthorityCapabilitiesAndEventCursor$' -count=1 -v

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
git diff --check -- internal/application/conversation_external_agents.go internal/application/conversation_agents.go internal/application/conversation_delegations.go internal/application/conversation_dispatch.go internal/application/conversation_service.go internal/application/conversation_tasks.go internal/infrastructure/persistence/database/agent/conversation_external_agent_store.go internal/infrastructure/persistence/database/agent/conversation_external_agent_store_test.go internal/infrastructure/persistence/database/agent/conversation_agents_store.go internal/infrastructure/persistence/database/agent/conversation_task_store.go internal/infrastructure/persistence/database/agent/conversation_peer_inbox_store.go internal/infrastructure/persistence/database/agent/conversation_collaboration_control_store.go internal/assembly/web/conversation_external_agent_test.go internal/transport/http/module/conversation_adapter.go remote/conversation_external_agents.go remote/conversation_external_agents_test.go server/server.go server/server_test.go frontend/src/task-state.ts frontend/src/collaboration-state.ts frontend/src/collaboration-state.test.ts frontend/src/TaskDialog.tsx frontend/src/CollaborationDialog.tsx
git -C ../domainry-agent-sdk diff --check -- conversation_external_agent.go conversation_external_agent_test.go conversation_collaboration.go conversation_task.go persistence/conversation_task.go conversation_http.go http_adapter_test.go
```

结果：Agent SDK 全包通过；Agent 的应用、持久化、Module HTTP、SaaS server／remote 通过。真实双用户链证明错误主体不能查询或认领，正确外部 Agent 可认领、消费消息、报告过程、提交结构化交付并由委派方验收。前端 94 项测试和生产构建通过，Vite 只报告既有大 chunk 提示。两个仓库的 K07 文件 diff 检查无错误。
