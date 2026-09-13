package application

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	tools "github.com/domainry/domainry-tools-sdk"
)

func scheduleResultReadDefinition(key string) (sdk.ConversationToolDefinition, bool) {
	for _, definition := range tools.ScheduleDefinitions() {
		if definition.Key == key {
			return definition, true
		}
	}
	return sdk.ConversationToolDefinition{}, false
}

// The Tools owner has already checked the complete plan result and current
// plan-reading permission. Its conversation reference grants no access to the
// private inputs from which a task/reminder was authored.
func (audit *conversationSourceAudit) scheduleToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	definition, known := scheduleResultReadDefinition(record.Call.Name)
	if !known || conversationDigest(definition) != conversationDigest(record.Definition) || record.Result == nil {
		return nil, invalidPersonalReceipt()
	}
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	ctx, audit = audit.rawExecutionSourceAudit(ctx)
	type origin struct {
		ConversationID string `json:"conversation_id"`
		RunID          string `json:"run_id"`
	}
	var saved struct {
		Plan  *origin  `json:"plan"`
		Items []origin `json:"items"`
	}
	// This projection extracts references after the source owner's closed
	// result validation; plan fields themselves remain owned by Tools/Scheduler.
	if json.Unmarshal(record.Result.Content, &saved) != nil || len(saved.Items) > 100 {
		return nil, invalidPersonalReceipt()
	}
	if saved.Plan != nil {
		saved.Items = append(saved.Items, *saved.Plan)
	}
	roots := []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}
	seen := map[origin]bool{}
	for _, source := range saved.Items {
		if seen[source] {
			continue
		}
		seen[source] = true
		if source.ConversationID == "" {
			if source.RunID != "" {
				return nil, invalidPersonalReceipt()
			}
			continue // Plans authored through the product API have no run.
		}
		if err := audit.authorizeDeliveredExecutionData(ctx, source.ConversationID); err != nil {
			return nil, err
		}
		if source.RunID == "" {
			continue
		}
		ref := sdk.ConversationRunReference{ConversationID: source.ConversationID, RunID: source.RunID}
		if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID {
			ref.BeforeStep = record.Step + 1
		}
		part, err := audit.run(ctx, ref)
		if err != nil {
			return nil, err
		}
		roots = mergeConversationSources(roots, part)
	}
	return roots, ctx.Err()
}
