package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
)

// Child traversals share completed proofs only inside this one read. Their
// exact ledger, reader and service-owned read boundaries remain cache keys.
func (audit *conversationSourceAudit) childSourceAudit() *conversationSourceAudit {
	child := audit.s.sourceAudit(audit.a, audit.conversationID)
	child.cache, child.reading, child.records = audit.cache, audit.reading, audit.records
	return child
}

func (audit *conversationSourceAudit) sourceCacheKey(ctx context.Context, ref sdk.ConversationRunReference) (string, error) {
	scope, scoped := ctx.Value(conversationPublishedSourceKey{}).(conversationPublishedSource)
	release, released := ctx.Value(conversationSourceReleaseKey{}).(conversationSourceRelease)
	origins := make([]sdk.ConversationAuthority, len(release.roots))
	for i, root := range release.roots {
		origins[i] = release.origins[root]
	}
	// These private context values affect publication selection, inherited
	// provenance, raw versus submitted text, and configured tool checks. A
	// completed delivery projection cannot authorize a raw history traversal.
	raw, err := json.Marshal([]any{
		ref, audit.a, audit.evidenceOwner, audit.conversationID,
		[]any{scoped, scope.purpose, scope.delegationID, scope.roots},
		[]any{released, release.reader, release.producer, release.active, release.purpose, release.delegationID, release.roots, origins},
		ctx.Value(conversationSourceParentKey{}),
		ctx.Value(conversationDeliverySourceKey{}),
		ctx.Value(conversationRawExecutionSourceKey{}),
		ctx.Value(conversationDeliveryHistorySourceKey{}),
		ctx.Value(conversationContractResultKey{}),
		ctx.Value(conversationAgentContextKey{}),
		ctx.Value(conversationAgentCatalogKey{}),
		ctx.Value(conversationPeerRequestKey{}),
	})
	if err != nil {
		return "", conversationFailure("unavailable", "source_reference_invalid")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
