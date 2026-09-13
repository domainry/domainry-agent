package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	orm "github.com/domainry/domainry-orm/schema"
)

const conversationSourceReleaseTable = "_agent_source_releases"

func conversationSourceReleasesMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := orm.NewTable(d, conversationSourceReleaseTable).IfNotExists().Columns(
		required("owner_key", orm.TextKey(64)), required("release_id", orm.TextKey(64)), required("producer_key", orm.TextKey(64)), required("delegation_id", orm.TextKey(96)), required("conversation_id", orm.TextKey(255)), required("run_id", orm.TextKey(255)), required("before_step", orm.BigInt()), required("payload_json", orm.LongText()),
	).PrimaryKey("owner_key", "release_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := orm.NewIndex(d, "idx_agent_source_release_v28", conversationSourceReleaseTable).Columns("owner_key", "conversation_id", "run_id", "before_step").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	producer, _, err := orm.NewIndex(d, "idx_agent_source_producer_v28", conversationSourceReleaseTable).Columns("producer_key", "delegation_id").Build()
	return modulehost.SchemaMigration{Version: 28, Name: "delegation_source_releases", Statements: []string{table, index, producer}}, err
}
