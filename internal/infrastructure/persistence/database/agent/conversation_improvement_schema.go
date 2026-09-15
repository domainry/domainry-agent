package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationCapabilityFeedbackTable   = "_agent_capability_feedback"
	conversationImprovementCandidateTable = "_agent_improvement_candidates"
	conversationCapabilityConfigTable     = "_agent_capability_configs"
)

func conversationImprovementMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	feedback, _, err := ormschema.NewTable(d, conversationCapabilityFeedbackTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("feedback_id", ormschema.TextKey(96)),
		required("client_id", ormschema.TextKey(96)), required("request_hash", ormschema.TextKey(64)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "feedback_id").Unique("owner_key", "client_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	candidates, _, err := ormschema.NewTable(d, conversationImprovementCandidateTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("candidate_id", ormschema.TextKey(96)),
		required("client_id", ormschema.TextKey(96)), required("kind", ormschema.TextKey(32)),
		required("target_key", ormschema.TextKey(96)), required("version", ormschema.TextKey(128)),
		required("status", ormschema.TextKey(32)), required("revision", ormschema.BigInt()),
		required("request_hash", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()),
		required("created_at", ormschema.BigInt()), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "candidate_id").Unique("owner_key", "client_id").Unique("owner_key", "kind", "target_key", "version").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	configs, _, err := ormschema.NewTable(d, conversationCapabilityConfigTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("kind", ormschema.TextKey(32)),
		required("target_key", ormschema.TextKey(96)), required("version", ormschema.TextKey(128)),
		required("candidate_id", ormschema.TextKey(96)), required("revision", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "kind", "target_key").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 35, Name: "agent_capability_improvements", Statements: []string{feedback, candidates, configs}}, nil
}
