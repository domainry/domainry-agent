// Package operationkernel installs the canonical shared Operations tables for
// the standalone Agent host. Embedded Agent modules receive these tables from
// Runtime and do not run this migration.
package operationkernel

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func SchemaMigration(renderer modulehost.Dialect) (modulehost.SchemaMigration, error) {
	migration := modulehost.SchemaMigration{Version: 1, Name: "shared_operations"}
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(renderer, "_operations").IfNotExists().Columns(
			required("id", ormschema.TextKey(255)),
			required("workspace_id", ormschema.TextKey(191)),
			defaulted("system_purpose", ormschema.TextKey(191), ""),
			required("owner", ormschema.TextKey(191)),
			required("kind", ormschema.TextKey(191)),
			required("action_key", ormschema.TextKey(255)),
			defaulted("parent_id", ormschema.TextKey(255), ""),
			required("resource_type", ormschema.TextKey(255)),
			defaulted("resource_id", ormschema.TextKey(255), ""),
			required("idempotency_key", ormschema.TextKey(191)),
			required("request_fingerprint", ormschema.TextKey(255)),
			required("requested_by", ormschema.TextKey(255)),
			required("reason", ormschema.LongText()),
			defaulted("reference", ormschema.TextKey(255), ""),
			required("status", ormschema.TextKey(191)),
			required("status_url", ormschema.LongText()),
			required("result_json", ormschema.LongText()),
			required("metadata_json", ormschema.LongText()),
			defaulted("error_code", ormschema.TextKey(255), ""),
			defaulted("failure_class", ormschema.TextKey(255), ""),
			defaulted("next_action", ormschema.LongText(), ""),
			required("related_ids_json", ormschema.LongText()),
			defaulted("correlation", ormschema.TextKey(255), ""),
			required("evidence_json", ormschema.LongText()),
			defaulted("lease_owner", ormschema.TextKey(255), ""),
			defaulted("lease_expires_at", ormschema.TextKey(191), ""),
			defaulted("fencing_token", ormschema.BigInt(), 0),
			defaulted("expires_at", ormschema.TextKey(191), ""),
			required("created_at", ormschema.TextKey(191)),
			defaulted("started_at", ormschema.TextKey(255), ""),
			defaulted("finished_at", ormschema.TextKey(255), ""),
			required("updated_at", ormschema.TextKey(191)),
		).PrimaryKey("id"),
		ormschema.NewTable(renderer, "_operation_controls").IfNotExists().Columns(
			required("system_purpose", ormschema.TextKey(255)),
			required("control_kind", ormschema.TextKey(255)),
			required("owner", ormschema.TextKey(255)),
			required("state", ormschema.TextKey(191)),
			required("reason", ormschema.Text()),
			defaulted("reference", ormschema.Text(), ""),
			required("updated_by", ormschema.Text()),
			required("revision", ormschema.BigInt()),
			required("updated_at", ormschema.Text()),
		),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return migration, err
		}
		migration.Statements = append(migration.Statements, statement)
	}
	indexes := []struct {
		name, table string
		unique      bool
		columns     []string
	}{
		{"uniq_runtime_operation_key", "_operations", true, []string{"workspace_id", "system_purpose", "owner", "kind", "idempotency_key"}},
		{"idx_runtime_operation_status", "_operations", false, []string{"workspace_id", "owner", "status", "created_at"}},
		{"idx_runtime_operation_parent", "_operations", false, []string{"workspace_id", "parent_id", "created_at"}},
		{"idx_runtime_operation_lease", "_operations", false, []string{"owner", "status", "lease_expires_at"}},
		{"uniq_runtime_operation_control", "_operation_controls", true, []string{"system_purpose", "control_kind", "owner"}},
		{"idx_runtime_operation_control_state", "_operation_controls", false, []string{"system_purpose", "control_kind", "state"}},
	}
	for _, index := range indexes {
		builder := ormschema.NewIndex(renderer, index.name, index.table).Columns(index.columns...)
		if index.unique {
			builder.Unique()
		}
		statement, _, err := builder.Build()
		if err != nil {
			return migration, err
		}
		migration.Statements = append(migration.Statements, statement)
	}
	return migration, nil
}

func required(name string, dataType ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, dataType).NotNull()
}

func defaulted(name string, dataType ormschema.ColumnType, value any) ormschema.ColumnDefinition {
	return ormschema.Column(name, dataType).NotNull().DefaultValue(value)
}
