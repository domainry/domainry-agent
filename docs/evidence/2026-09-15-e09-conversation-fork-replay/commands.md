# E09 验收命令

日期：2026-09-15。历史证据目录包含归档源码，因此产品包清单明确排除 `docs/evidence`。

## SDK 与产品回归

```bash
cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...
git diff --check

cd /Users/tiger/Projects/domainry-agent
go list -e -f '{{.ImportPath}}' ./... | rg -v '^$|/docs/evidence/|/integration$|/internal/assembly/web$' | xargs go test -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1 -timeout 15m
git diff --check
```

## 分叉、回放与授权 race

```bash
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./integration \
  -run 'TestConversationTrajectory|TestConversationFork' -count=1
```

## 前端与真实浏览器

```bash
cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
AGENT_E09_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/Users/tiger/Projects/domainry-agent/docs/evidence/2026-09-15-e09-conversation-fork-replay/browser \
go test ./internal/assembly/web -run '^TestConversationTrajectoryForkReplayBuiltBrowser$' -count=1 -timeout 6m -v
```

## 固定对照源码

- DeepSeek Harness commit：`c291e7961a515f6d7af9304e7fd1d257929aef26`
- `docs/subsystems/session.md` SHA-256：`dfabaaf8c1dae793e1ee6752c53b50a575c5d8b1cbc86a0b824e98c4e55d665f`
- `packages/test-support/llm-replay/README.md` SHA-256：`ebbacf0a0926b4e3398d62220b38d59ebde6f1fbbd3243673ced23f2f7fde30f`
