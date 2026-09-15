package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (audit *conversationSourceAudit) deliveryBusinessToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	definition, business := businessTool(record.Call.Name)
	if !business || scope == "" {
		return false, nil
	}
	if conversationDigest(definition) != conversationDigest(record.Definition) {
		return true, conversationFailure("conflict", "tool_changed")
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return true, err
	}
	if err := audit.connectedTool(ctx, record.Call.Name); err != nil {
		return true, err
	}
	source := audit.s.options.Business
	if source == nil {
		return true, conversationFailure("forbidden", "business_access_denied")
	}
	if record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return true, conversationFailure("conflict", "business_response_invalid")
	}
	var evidence sdk.ConversationBusinessEvidence
	decoder := json.NewDecoder(bytes.NewReader(record.Result.Content))
	decoder.DisallowUnknownFields()
	producer := audit.evidenceAuthority(owner)
	if !json.Valid(record.Result.Content) || decoder.Decode(&evidence) != nil || evidence.Version != 1 || evidence.Operation != record.Call.Name || evidence.Source != source.BusinessSourceIdentity() || evidence.ScopeSHA256 != businessEvidenceScope(evidence.Source, producer) || !json.Valid(evidence.Input) || !json.Valid(evidence.Data) || !json.Valid([]byte(record.Call.Arguments)) || conversationDigest(evidence.Input) != conversationDigest(json.RawMessage(record.Call.Arguments)) {
		return true, conversationFailure("conflict", "business_response_invalid")
	}
	ctx, cancel := audit.s.externalCallContext(ctx, 0)
	defer cancel()
	err := sdk.AuthorizeSharedBusinessResultRead(ctx, source, evidence, audit.a, producer)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	var coded *sdk.Error
	if errors.As(err, &coded) && coded.Class == "unavailable" && coded.Code == sdk.BusinessResultReadUnsupportedCode {
		return false, nil
	}
	if err != nil {
		return true, conversationFailure("forbidden", businessFailureCode(err))
	}
	if source.BusinessSourceIdentity() != evidence.Source {
		return true, conversationFailure("conflict", "business_source_changed")
	}
	return true, ctx.Err()
}
