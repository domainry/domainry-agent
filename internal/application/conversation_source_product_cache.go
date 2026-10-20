package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// A source graph can reach the same completed CRM read through several run
// prefixes. The cutoff selects which calls to visit, not the authorization of
// an individual call: ToolRequest contains its exact run/step/call, not a cutoff.
// Reuse that successful check inside this one source audit only, just like the
// existing run-level memo. Never share a decision across a model wait/request.
func (audit *conversationSourceAudit) record(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (roots []sdk.ConversationRunReference, err error) {
	ctx, finish := audit.s.beginSourceAuthorizationEvaluation(ctx)
	defer func() {
		if checkErr := finish(); err == nil && checkErr != nil {
			roots, err = nil, checkErr
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !audit.memoizedProductRead(record) || releasedSourcePurpose(ctx) != "" {
		return audit.recordCurrent(ctx, owner, record)
	}
	frame, err := audit.sourceCacheKey(ctx, sdk.ConversationRunReference{ConversationID: owner.ConversationID, RunID: owner.RunID})
	if err != nil {
		return nil, err
	}
	key := conversationDigest([]any{frame, conversationRecordHash(record)})
	if roots, ok := audit.productReads[key]; ok {
		return append([]sdk.ConversationRunReference(nil), roots...), nil
	}
	roots, err = audit.recordCurrent(ctx, owner, record)
	if err == nil {
		if audit.productReads == nil {
			audit.productReads = make(map[string][]sdk.ConversationRunReference)
		}
		if len(audit.productReads) < 256 {
			audit.productReads[key] = append([]sdk.ConversationRunReference(nil), roots...)
		}
	}
	return roots, err
}

func (audit *conversationSourceAudit) memoizedProductRead(record persistence.ConversationToolExecution) bool {
	if record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Definition.Effect != "read" {
		return false
	}
	// Core tools have their own provenance, release and traversal semantics.
	// Restrict this memo to exact source-owned registered product contracts.
	if _, ok := businessTool(record.Call.Name); ok {
		return false
	}
	if _, ok := knowledgeTool(record.Call.Name); ok {
		return false
	}
	if _, ok := artifactTool(record.Call.Name); ok {
		return false
	}
	if _, ok := scheduleResultReadDefinition(record.Call.Name); ok {
		return false
	}
	for _, definition := range sdk.PersonalConversationTools() {
		if definition.Key == record.Call.Name {
			return false
		}
	}
	for _, definition := range audit.s.options.ToolDefinitions {
		if definition.Key == record.Call.Name && conversationDigest(definition) == conversationDigest(record.Definition) {
			return true
		}
	}
	return false
}
