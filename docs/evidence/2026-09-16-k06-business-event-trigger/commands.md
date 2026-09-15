# K06 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-16。

```sh
cd /Users/tiger/Projects/domainry-integration-sdk
go test ./...

cd /Users/tiger/Projects/domainry-integration
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/domain/integration/model ./internal/infrastructure/persistence/database/integration ./internal/transport/http/module -count=1

cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./server ./module ./internal/assembly/web -run 'TestBusinessEvent|TestRemoteFactoryBuildsIndependentBusinessEventTaskService|TestServerCapabilityAndServiceActions' -count=1
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/assembly/web -run '^TestBusinessEventCreatesAndWakesAgentTaskThroughCurrentIdentity$' -count=1 -v

cd /Users/tiger/Projects/domainry-runtime
GOWORK=/tmp/domainry-k06.work go test ./runtime/bootstrap/composition ./runtime/bootstrap/runtime ./runtime/domain/appschema/contract ./runtime/domain/manifest/validation -run 'TestRuntimeIntegrationTriggerSink|TestManifestIntegrationAgentEvent|TestRuntimeServices|TestAppSchema' -count=1

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
git diff --check -- internal/application/conversation_tasks.go internal/infrastructure/persistence/database/agent/conversation_content.go internal/infrastructure/persistence/database/agent/conversation_task_store.go internal/infrastructure/persistence/database/agent/conversation_task_store_test.go internal/assembly/web/conversation_business_event_test.go internal/application/conversation_dispatch.go remote/conversations.go remote/factory_test.go server/authorization_actions.go server/conversations.go server/server.go server/server_test.go module/factory.go frontend/src/task-state.ts frontend/src/TaskDialog.tsx
git -C ../domainry-agent-sdk diff --check -- conversation_task.go conversation_task_test.go conversation_http.go persistence/conversation_task.go
git -C ../domainry-integration diff --check -- internal/domain/integration/model/integration_operations.go internal/domain/integration/model/integration_event_mapping_validation.go internal/infrastructure/persistence/database/integration/inbound_store.go internal/infrastructure/persistence/database/integration/operations_store_test.go internal/transport/http/module/capability.go
git -C ../domainry-integration-sdk diff --check -- operations.go sdk_test.go
git -C ../domainry-runtime diff --check -- runtime/domain/appschema/model/appschema_integration_definition.go runtime/domain/appschema/contract/appschema_integration_event_binding_contract.go runtime/domain/manifest/validation/manifest_validator_refs.go runtime/domain/manifest/validation/manifest_validation_edges_test.go runtime/bootstrap/composition/runtime_services_state.go runtime/bootstrap/composition/runtime_services.go runtime/bootstrap/composition/runtime_services_initialization.go runtime/bootstrap/runtime/service_assembly.go runtime/bootstrap/runtime/startup.go runtime/bootstrap/runtime/integration_trigger_sink.go runtime/bootstrap/runtime/integration_trigger_sink_test.go
```

测试结果：

- Integration SDK 全包通过；Integration 当前源码的事件映射、持久队列与 HTTP 包通过。
- Agent SDK 全包通过；Agent 的服务动作、远端绑定、事务幂等和真实 Identity／HTTP／SQLite／worker 链路通过。
- Runtime 的 Agent 事件路由、当前 Identity 映射、manifest 有限目标校验与服务装配通过。
- 前端 93 项测试通过，生产构建通过；Vite 仅报告既有大 chunk 提示。
- 五个仓库的 K06 文件 diff 检查均无错误。

Integration 不指定 `GOWORK` 时会解析已发布的 `domainry-integration-sdk v0.1.6` 和 `domainry-connector-sdk v0.1.1`，分别缺少本轮 Agent 事件字段和先前 MCP 包。当前源码联调使用 Agent 仓库现有 `go.work`；这属于跨仓发布顺序，不是 K06 当前源码失败。
