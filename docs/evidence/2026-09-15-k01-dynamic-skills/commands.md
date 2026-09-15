# K01 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-15。

```sh
cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
go test ./internal/application -count=1
go test ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./remote ./integration -run 'Skill|Improvement|CapabilityFeedback|DynamicSkill|ScheduledConversationTask' -count=1

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
AGENT_K01_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/domainry-k01-dynamic-skill-browser-20260915j \
GOWORK=/Users/tiger/Projects/domainry-agent/go.work \
go test ./internal/assembly/web -run '^TestDynamicSkillFeedbackPublicationRollbackBuiltBrowser$' -count=1 -timeout 6m -v

git diff --check
cd /Users/tiger/Projects/domainry-agent-sdk
git diff --check
```

日志位于 `logs/`；浏览器截图和机器可读结果位于 `browser/`。SDK 命令显式使用项目 `go.work`，以采用本批共同修改的本地 Agent SDK 与 Tools SDK。
