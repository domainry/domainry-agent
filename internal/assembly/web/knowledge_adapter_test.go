package web

import (
	knowledgehttpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func newKnowledgeWithOfficialAdapter(config knowledgemodule.KnowledgeConfig) (*knowledgemodule.Knowledge, error) {
	return knowledgemodule.NewKnowledge(config, knowledgehttpapi.New)
}
