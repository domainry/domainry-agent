package application

import (
	"context"
	"errors"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// Only a service-authorized delivery projection can use the independent
// source read policy. Local knowledge, private files and collaboration tools
// retain their own reference traversal and are not treated as opaque outputs.
func (audit *conversationSourceAudit) deliveryToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	if scope == "" {
		return false, nil
	}
	if definition, known := scheduleResultReadDefinition(record.Call.Name); known {
		if handled, err := audit.attestDeliveryPersonalRecord(ctx, owner, record, definition); !handled || err != nil {
			return handled, err
		}
	}
	if _, local := knowledgeTool(record.Call.Name); local {
		return false, nil
	}
	if _, local := artifactTool(record.Call.Name); local {
		return false, nil
	}
	if _, local := businessTool(record.Call.Name); local {
		return false, nil
	}
	for _, definition := range sdk.PersonalConversationTools() {
		if definition.Key == record.Call.Name {
			return false, nil
		}
	}
	registered := false
	for _, definition := range audit.s.options.ToolDefinitions {
		if definition.Key == record.Call.Name {
			if conversationDigest(definition) != conversationDigest(record.Definition) {
				return true, conversationFailure("conflict", "tool_changed")
			}
			registered = true
			break
		}
	}
	if !registered {
		return false, nil
	}
	if reader, ok := audit.s.options.ToolAvailability.(toolsdk.ResultReadAvailability); ok {
		checkCtx, cancel := audit.s.externalCallContext(ctx, 5*time.Second)
		ready, err := reader.ConversationToolResultReadAvailable(checkCtx, audit.a, record.Call.Name)
		if err == nil {
			err = checkCtx.Err()
		}
		cancel()
		if err != nil || !ready {
			return true, conversationFailure("unavailable", "tool_unavailable")
		}
	} else if err := audit.connectedTool(ctx, record.Call.Name); err != nil {
		return true, err
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return true, err
	}
	// Preserve the original operation identity for source-owned receipt checks,
	// without handing a reader any worker lease or execution confirmation.
	request := sdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, CorrelationID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition, IdempotencyKey: record.IdempotencyKey}
	if producer := audit.evidenceAuthority(owner); producer.UserID != audit.a.UserID || producer.RoleKey != audit.a.RoleKey {
		request.ResultProducer = &producer
	}
	ctx, cancel := audit.s.externalCallContext(ctx, time.Duration(record.Definition.TimeoutMillis)*time.Millisecond)
	defer cancel()
	err := authorizeToolResultRead(ctx, audit.s.options.ToolHost, request, *record.Result)
	var coded *toolsdk.Error
	if errors.As(err, &coded) && coded.Class == "unavailable" && coded.Code == toolsdk.ResultReadUnsupportedCode {
		// Unmigrated tools still need both their original execution grant and
		// existing result/source checks. An explicit source denial cannot fall
		// back to that path, even if the caller can execute the tool.
		return false, nil
	}
	return true, err
}

func authorizeToolResultRead(ctx context.Context, host sdk.ConversationToolHost, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
	if policy, ok := host.(sdk.ConversationToolResultReadAuthorizer); ok {
		return policy.AuthorizeConversationToolResultRead(ctx, in, result)
	}
	return &toolsdk.Error{Class: "unavailable", Code: toolsdk.ResultReadUnsupportedCode}
}

// Profile tools are execution capabilities. Reading a submitted result still
// checks the selected owner's registration and current source policy, but does
// not require loading the producer's tools into the reader's execution profile.
func (h *profileToolHost) AuthorizeConversationToolResultRead(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
	return authorizeToolResultRead(ctx, h.base, in, result)
}

func (h *extensionInteractionHost) AuthorizeConversationToolResultRead(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
	return authorizeToolResultRead(ctx, h.ConversationToolHost, in, result)
}

func (h *assemblyConfirmationHost) AuthorizeConversationToolResultRead(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
	return authorizeToolResultRead(ctx, h.ConversationToolHost, in, result)
}
