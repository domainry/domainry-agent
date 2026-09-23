package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const agentOperationReceiptTable = "_agent_owner_operation_receipts"

func subjectLifecycleMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	agentOperationReceipt, _, err := ormschema.NewTable(d, agentOperationReceiptTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("request_id", ormschema.TextKey(96)),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "request_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	knowledgeOperations, err := knowledgemodule.ConversationReferenceLifecycleMigration(d)
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	statements := []string{agentOperationReceipt}
	statements = append(statements, knowledgeOperations.Statements...)
	return modulehost.SchemaMigration{Version: 18, Name: "agent_owner_operation_receipts", Statements: statements}, nil
}
