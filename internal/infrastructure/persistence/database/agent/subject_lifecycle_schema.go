package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	ormschema "github.com/domainry/domainry-orm/schema"
	todomodule "github.com/domainry/domainry-todo/module"
)

const (
	agentSubjectReceiptTable   = "_agent_subject_erasure_receipts"
	agentOperationReceiptTable = "_agent_owner_operation_receipts"
)

func subjectLifecycleMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	agentReceipt, _, err := ormschema.NewTable(d, agentSubjectReceiptTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("request_id", ormschema.TextKey(96)),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "request_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	agentOperationReceipt, _, err := ormschema.NewTable(d, agentOperationReceiptTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("request_id", ormschema.TextKey(96)),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "request_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	todoReceipt, err := todomodule.SubjectLifecycleMigration(d)
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	knowledgeReceipt, err := knowledgemodule.SubjectLifecycleMigration(d)
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	knowledgeOperations, err := knowledgemodule.ConversationReferenceLifecycleMigration(d)
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	statements := []string{agentReceipt, agentOperationReceipt}
	statements = append(statements, todoReceipt.Statements...)
	statements = append(statements, knowledgeReceipt.Statements...)
	statements = append(statements, knowledgeOperations.Statements...)
	return modulehost.SchemaMigration{Version: 18, Name: "agent_subject_lifecycle_receipts", Statements: statements}, nil
}
