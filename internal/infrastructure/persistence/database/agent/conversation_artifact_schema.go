package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationArtifactMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 6, Name: "agent_conversation_artifacts"}
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(d, "_agent_artifacts").IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("artifact_id", ormschema.TextKey(96)), required("version", ormschema.BigInt()), required("created_at", ormschema.BigInt()), required("source_conversation_id", ormschema.TextKey(96)), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "artifact_id"),
		ormschema.NewTable(d, "_agent_artifact_versions").IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("artifact_id", ormschema.TextKey(96)), required("version", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "artifact_id", "version"),
		ormschema.NewTable(d, "_agent_artifact_mutations").IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("client_key", ormschema.TextKey(64)), required("created_at", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "client_key"),
		ormschema.NewTable(d, "_agent_artifact_exports").IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("export_id", ormschema.TextKey(96)), required("artifact_id", ormschema.TextKey(96)), required("expires_at", ormschema.BigInt()), required("download_count", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "export_id"),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	statement, _, err := ormschema.NewIndex(d, "idx_agent_artifacts_owner_v6", "_agent_artifacts").Columns("owner_key", "created_at", "artifact_id").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	return m, nil
}
