package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

// agentRunMemoryCandidateMigration keeps the published foundation migration
// immutable while adding the indexed candidate state used by governed memory.
func agentRunMemoryCandidateMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	column, _, err := ormschema.NewAddColumn(d, agentRunTable, optional("memory_candidate_state", ormschema.TextKey(16))).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_run_memory_candidates_v37", agentRunTable).
		Columns("run_kind", "memory_candidate_state", "runtime_id", "updated_at").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 37, Name: "agent_run_memory_candidates", Statements: []string{column, index}}, nil
}
