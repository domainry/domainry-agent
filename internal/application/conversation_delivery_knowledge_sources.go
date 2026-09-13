package application

import (
	"context"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (audit *conversationSourceAudit) deliveryKnowledgeToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	if _, knowledge := knowledgeTool(record.Call.Name); !knowledge || scope == "" || privateRemoteAttachmentCall(record.Call) {
		return false, nil
	}
	if !knownKnowledgeDefinition(record.Definition) || record.Definition.Key != record.Call.Name {
		return true, conversationFailure("conflict", "tool_changed")
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return true, err
	}
	if record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return true, conversationFailure("conflict", "knowledge_response_invalid")
	}
	if err := audit.connectedTool(ctx, record.Call.Name); err != nil {
		return true, err
	}
	source, ok := audit.s.options.Knowledge.(sdk.ConversationKnowledgeSource)
	if !ok {
		return true, conversationFailure("forbidden", "knowledge_access_denied")
	}
	host := knowledgeConversationHost{source: source}
	request := sdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, CorrelationID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
	ctx, cancel := audit.s.externalCallContext(ctx, 0)
	defer cancel()
	err := host.authorizeKnowledgeResult(ctx, request, *record.Result, true)
	var coded *sdk.Error
	if errors.As(err, &coded) && coded.Class == "unavailable" && coded.Code == sdk.KnowledgeResultReadUnsupportedCode {
		// Only an explicitly unsupported source retains the old, fully
		// authorized execution-read path. A source denial never falls back.
		return false, nil
	}
	if err == nil {
		err = ctx.Err()
	}
	return true, err
}
