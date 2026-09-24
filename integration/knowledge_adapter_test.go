package integration_test

import (
	knowledgehttpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
	knowledgeprovider "github.com/domainry/domainry-knowledge-sdk/provider"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func newKnowledgeWithOfficialAdapter(config knowledgeprovider.Config) (*knowledgemodule.Knowledge, error) {
	return knowledgemodule.NewKnowledge(config, knowledgehttpapi.New)
}
