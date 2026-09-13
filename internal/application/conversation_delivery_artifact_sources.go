package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (audit *conversationSourceAudit) deliveryArtifactToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, []sdk.ConversationRunReference, error) {
	scope := releasedSourcePurpose(ctx)
	if _, artifact := artifactTool(record.Call.Name); !artifact || scope == "" {
		return false, nil, nil
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return true, nil, err
	}
	if record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return true, nil, conversationFailure("conflict", "artifact_result_invalid")
	}
	// Preserve the user's choice to disable the producing tool as well as the
	// resource reader, independently of the original creation/edit/export grant.
	for _, key := range []string{record.Call.Name, "artifact_read"} {
		if err := audit.connectedTool(ctx, key); err != nil {
			return true, nil, err
		}
	}
	ctx, cancel := audit.s.externalCallContext(ctx, 0)
	defer cancel()
	roots, err := audit.artifactToolResult(ctx, owner, record, true)
	if err == nil {
		err = ctx.Err()
	}
	return true, roots, err
}
