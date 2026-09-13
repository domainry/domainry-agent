package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	tools "github.com/domainry/domainry-tools-sdk"
)

type deliveryReadTestHost struct {
	sdk.ConversationToolHost
	reads, executionChecks int
	err                    error
	lastRequest            sdk.ConversationToolRequest
}

type deliveryReadAvailabilityProbe struct {
	ready                       bool
	executionChecks, readChecks int
}

func TestDeliveryToolReadKeepsReaderAuthoritySeparateFromProducerProof(t *testing.T) {
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", ActionKey: "specialist.execute", TimeoutMillis: 1000}
	host := &deliveryReadTestHost{}
	reader := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader", RoleKey: "read-role"}
	producer := reader
	producer.UserID, producer.RoleKey = "producer", "professional-role"
	s := &ConversationService{runtimeID: "runtime", repo: privatePeerSources{}, options: ConversationOptions{ToolDefinitions: []sdk.ConversationToolDefinition{definition}, ToolHost: host, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
	audit := s.sourceAudit(reader)
	audit.evidenceOwner = &producer
	record := persistence.ConversationToolExecution{Definition: definition, Step: 2, Call: sdk.ConversationToolCall{ID: "original-call", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"value":"9007199254740993"}`)}, IdempotencyKey: "original-key", LeaseOwner: "original-worker", Fence: 4}
	owner := sdk.ConversationRunReference{ConversationID: "producer-conversation", RunID: "original-run"}
	ctx := deliverySourceContext(t.Context(), "released")
	if _, err := audit.record(ctx, owner, record); err != nil {
		t.Fatal(err)
	}
	request := host.lastRequest
	if request.Authority != reader || request.ResultProducer == nil || *request.ResultProducer != producer || request.IdempotencyKey != record.IdempotencyKey || request.Step != record.Step || request.LeaseOwner != "" || request.Fence != 0 || request.Confirmation != nil || host.executionChecks != 0 {
		t.Fatal("producer proof became execution authority", request, host.executionChecks)
	}
	host.err = &tools.Error{Class: "forbidden", Code: "actual-reader-data-revoked"}
	if _, err := audit.record(ctx, owner, record); err == nil || host.executionChecks != 0 {
		t.Fatal("reader denial fell back to producer execution rights", err)
	}
}

func (p *deliveryReadAvailabilityProbe) ConversationToolAvailable(context.Context, tools.Authority, string) (bool, error) {
	p.executionChecks++
	return false, nil
}

func (p *deliveryReadAvailabilityProbe) ConversationToolResultReadAvailable(ctx context.Context, _ tools.Authority, _ string) (bool, error) {
	p.readChecks++
	return p.ready, ctx.Err()
}

func (h *deliveryReadTestHost) AuthorizeConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	h.executionChecks++
	return sdk.ConversationToolAuthorization{}, nil
}
func (h *deliveryReadTestHost) AuthorizeConversationToolResultRead(_ context.Context, in sdk.ConversationToolRequest, _ sdk.ConversationToolResult) error {
	h.reads++
	h.lastRequest = in
	return h.err
}

func TestDeliveryToolReadPreservesScopeAndCurrentSourceDenial(t *testing.T) {
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", ActionKey: "specialist.execute", TimeoutMillis: 1000}
	base := &deliveryReadTestHost{}
	wrapped := &profileToolHost{base: &extensionInteractionHost{ConversationToolHost: &assemblyConfirmationHost{ConversationToolHost: base}}, allowed: map[string]bool{}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	s := &ConversationService{runtimeID: "runtime", repo: privatePeerSources{}, options: ConversationOptions{ToolDefinitions: []sdk.ConversationToolDefinition{definition}, ToolHost: wrapped, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
	audit := s.sourceAudit(a)
	owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
	record := persistence.ConversationToolExecution{Definition: definition, Call: sdk.ConversationToolCall{ID: "read", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{}`)}}
	record.IdempotencyKey, record.LeaseOwner, record.Fence = "original-call-key", "worker", 7
	if _, err := audit.record(t.Context(), owner, record); err == nil || base.reads != 0 {
		t.Fatal("raw source replay used independent delivery reading")
	}
	ctx := deliverySourceContext(t.Context(), "released")
	if _, err := audit.record(ctx, owner, record); err != nil || base.reads != 1 {
		t.Fatalf("execution profile incorrectly blocked source reading: %v", err)
	}
	if base.lastRequest.IdempotencyKey != record.IdempotencyKey || base.lastRequest.Authority != a || base.lastRequest.LeaseOwner != "" || base.lastRequest.Fence != 0 || base.lastRequest.Confirmation != nil {
		t.Fatalf("source reading lost operation identity or acquired execution authority: %+v", base.lastRequest)
	}
	availability := &deliveryReadAvailabilityProbe{ready: true}
	s.options.ToolAvailability = availability
	if _, err := audit.record(ctx, owner, record); err != nil || availability.executionChecks != 0 || availability.readChecks != 1 {
		t.Fatalf("delivery used execution catalog instead of source readiness: %v; %+v", err, availability)
	}
	availability.ready = false
	readsBefore := base.reads
	if _, err := audit.record(ctx, owner, record); err == nil || base.reads != readsBefore {
		t.Fatalf("disabled source reached result reader: %v", err)
	}
	availability.ready = true
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := audit.record(cancelled, owner, record); err == nil || base.reads != readsBefore {
		t.Fatalf("cancelled readiness reached result reader: %v", err)
	}
	base.err = &tools.Error{Class: "forbidden", Code: "source.revoked"}
	before := base.executionChecks
	if _, err := audit.record(ctx, owner, record); err != base.err || base.executionChecks != before {
		t.Fatalf("source denial downgraded to execution path: %v", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view"}
	reads := base.reads
	if _, err := audit.record(ctx, owner, record); !collaborationDenied(err) || base.reads != reads {
		t.Fatalf("revoked delivery reached source reader: %v", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
	base.err = &tools.Error{Class: "unavailable", Code: tools.ResultReadUnsupportedCode}
	if _, err := audit.record(ctx, owner, record); err == nil {
		t.Fatal("unsupported read implicitly granted access")
	}
}
