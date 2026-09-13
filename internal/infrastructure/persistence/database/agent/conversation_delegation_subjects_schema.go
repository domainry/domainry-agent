package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	orm "github.com/domainry/domainry-orm/schema"
)

const conversationDelegationSubjectTable = "_agent_delegation_subjects"

func conversationDelegationSubjectsMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := orm.NewTable(d, conversationDelegationSubjectTable).IfNotExists().Columns(
		required("owner_key", orm.TextKey(64)), required("delegation_id", orm.TextKey(96)),
		required("execution_owner_key", orm.TextKey(64)), required("source_authority_json", orm.LongText()), required("execution_authority_json", orm.LongText()), required("created_at", orm.BigInt()),
	).PrimaryKey("owner_key", "delegation_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := orm.NewIndex(d, "idx_agent_delegation_subject_v27", conversationDelegationSubjectTable).Columns("execution_owner_key", "created_at", "delegation_id").Build()
	return modulehost.SchemaMigration{Version: 27, Name: "delegation_execution_subjects", Statements: []string{table, index}}, err
}
