package integration_test

import (
	agentmodule "github.com/domainry/domainry-agent/module"
	knowledgehttpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	todomodule "github.com/domainry/domainry-todo/module"
)

func newAgentModuleFactory(options agentmodule.Options) *agentmodule.Factory {
	options.KnowledgeFactory = knowledgemodule.NewFactory()
	options.KnowledgeProviderFactory = knowledgemodule.NewProviderFactory(knowledgehttpapi.New)
	options.TodoFactory = todomodule.NewFactory()
	return agentmodule.NewFactory(options)
}
