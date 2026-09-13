package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type peerDisagreementWire struct {
	results map[string]sdk.ConversationToolResult
	partial map[string]string
	pending map[string]sdk.ConversationResultRead
}

type peerDisagreementToolUpdate struct {
	ExpectedRevision int64                               `json:"expected_revision"`
	Action           string                              `json:"action"`
	Reason           string                              `json:"reason"`
	Disagreement     *sdk.ConversationDisagreementChange `json:"disagreement"`
}

// This provider fixture reads the same JSON as a remote model: no direct store
// access and no internal ResultReference/ContextSources shortcuts.
func (m *peerReviewIssuerModel) resolveFixtureDisagreementStep(in sdk.ConversationStepRequest) (sdk.ConversationStepResult, bool) {
	id := ""
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Resolve disagreement fixture:\n") {
			id = strings.TrimPrefix(message.Content, "Resolve disagreement fixture:\n")
		}
	}
	if id == "" {
		return sdk.ConversationStepResult{}, false
	}
	stop := sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "分歧已记录并按证据对照；交付仍需明确验收。"}}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disagreementWire == nil {
		m.disagreementWire = map[string]*peerDisagreementWire{}
	}
	state := m.disagreementWire[id]
	if state == nil {
		state = &peerDisagreementWire{results: map[string]sdk.ConversationToolResult{}, partial: map[string]string{}, pending: map[string]sdk.ConversationResultRead{}}
		m.disagreementWire[id] = state
	}
	for _, message := range in.Messages {
		if message.Role != "tool" || !strings.HasPrefix(message.ToolCallID, "dispute-") {
			continue
		}
		var wire struct {
			sdk.ConversationToolResult
			Representation string                          `json:"representation"`
			Reference      sdk.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" {
			stop.Message.Content = "Fixture tool failed: " + message.ToolCallID + " " + message.Content
			return stop, true
		}
		if strings.HasPrefix(message.ToolCallID, "dispute-page-") {
			if wire.Representation != "" {
				continue
			} // An earlier page was already consumed into the fixture's wire buffer.
			var page sdk.ConversationResultSlice
			if json.Unmarshal(wire.Content, &page) != nil {
				stop.Message.Content = "Malformed result page"
				return stop, true
			}
			target := page.Reference.CallID
			if _, exists := state.results[target]; exists || page.Offset != len(state.partial[target]) {
				continue
			}
			state.partial[target] += page.JSONText
			if page.Complete {
				var result sdk.ConversationToolResult
				if json.Unmarshal([]byte(state.partial[target]), &result) != nil {
					stop.Message.Content = "Malformed reconstructed result"
					return stop, true
				}
				state.results[target] = result
				delete(state.pending, target)
			} else {
				state.pending[target] = sdk.ConversationResultRead{Reference: page.Reference, Offset: page.NextOffset, MaxBytes: 8192}
			}
			continue
		}
		if _, exists := state.results[message.ToolCallID]; exists {
			continue
		}
		if wire.Representation == "stored_result_preview" {
			if _, exists := state.pending[message.ToolCallID]; !exists {
				state.pending[message.ToolCallID] = sdk.ConversationResultRead{Reference: wire.Reference, MaxBytes: 8192}
			}
		} else {
			state.results[message.ToolCallID] = wire.ConversationToolResult
			delete(state.pending, message.ToolCallID)
		}
	}
	for _, target := range []string{"dispute-get", "dispute-raise", "dispute-read", "dispute-decide"} {
		if read, exists := state.pending[target]; exists {
			raw, _ := json.Marshal(read)
			call := sdk.ConversationToolCall{ID: fmt.Sprintf("dispute-page-%s-%d", target, read.Offset), Name: "tool_result_read", Arguments: string(raw)}
			return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, true
		}
	}
	if _, done := state.results["dispute-decide"]; done {
		return stop, true
	}
	var d sdk.ConversationDelegationDetail
	var history sdk.ConversationDisagreementHistory
	if result, found := state.results["dispute-get"]; found {
		_ = unmarshalPeerDetail(result.Content, &d)
	}
	result, raised := state.results["dispute-raise"]
	if raised {
		_ = unmarshalPeerDetail(result.Content, &d)
	}
	result, read := state.results["dispute-read"]
	if read {
		_ = json.Unmarshal(result.Content, &history)
	}
	call := sdk.ConversationToolCall{ID: "dispute-get", Name: "delegation_get", Arguments: `{"id":"` + id + `"}`}
	if d.ID != "" && !raised {
		var refs []sdk.ConversationResultReference
		for _, check := range d.Verification.Checks {
			refs = append(refs, check.Receipts...)
		}
		claims := []sdk.ConversationDisagreementClaimInput{
			{Conclusion: "The timestamp belongs to this execution", DataScope: "Clock read for this delegation", Period: "Current execution", SourceVersion: "Immutable time_now receipt", Calculation: "Match receipt conversation, run and step to the delivered task", Receipts: refs},
			{Conclusion: "The timestamp may belong to an earlier execution", DataScope: "Clock read for this delegation", Period: "Earlier execution, unverified", SourceVersion: "Alternative interpretation of the same receipt", Calculation: "Compare run identifiers before adopting the timestamp"},
		}
		raw, _ := json.Marshal(map[string]any{"id": id, "update": peerDisagreementToolUpdate{ExpectedRevision: d.Revision, Action: "disagreement", Reason: "Preserve both interpretations for review", Disagreement: &sdk.ConversationDisagreementChange{Operation: "raise", Title: "Which execution owns the clock receipt?", Claims: claims}}})
		call = sdk.ConversationToolCall{ID: "dispute-raise", Name: "delegation_update", Arguments: string(raw)}
	} else if raised && !read {
		if len(d.Disagreements) != 1 {
			stop.Message.Content = "Fixture expected current issue after raise"
			return stop, true
		}
		raw, _ := json.Marshal(sdk.ConversationDisagreementRead{ID: id, DisagreementID: d.Disagreements[0].ID})
		call = sdk.ConversationToolCall{ID: "dispute-read", Name: "delegation_disagreement", Arguments: string(raw)}
	} else if read {
		if len(history.Items) == 0 || len(history.Items[0].Claims) < 2 {
			return stop, true
		}
		issue := history.Items[0]
		decision := sdk.ConversationDisagreementDecision{Outcome: "adopt", AdoptClaimID: issue.Claims[0].ID, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, DeliveryDigest: d.Verification.DeliveryDigest, Basis: "The immutable receipt belongs to the delivered task execution", Comparison: sdk.ConversationEvidenceComparison{DataScope: "Both claims refer to the same delegation clock read", Period: "Receipt identifies the current execution; the earlier-run claim has no matching receipt", SourceVersion: "Compare the immutable receipt SHA and its saved execution", Calculation: "Match conversation ID, run ID and tool step; no majority or arrival-order rule"}}
		raw, _ := json.Marshal(map[string]any{"id": id, "update": peerDisagreementToolUpdate{ExpectedRevision: d.Revision, Action: "disagreement", Reason: "Adopt the conclusion supported by the original execution", Disagreement: &sdk.ConversationDisagreementChange{Operation: "decide", ID: issue.ID, ExpectedRevision: issue.Revision, Decision: &decision}}})
		call = sdk.ConversationToolCall{ID: "dispute-decide", Name: "delegation_update", Arguments: string(raw)}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, true
}

func verifyPeerDisagreementAgent(t *testing.T, b *browser, sourceID string, d *sdk.ConversationDelegationDetail) sdk.ConversationDisagreementHistory {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var source sdk.Conversation
		json.Unmarshal(b.call("GET", "/agent/conversations/"+sourceID, "", 200).Body.Bytes(), &source)
		if source.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("issuer remained active before disagreement review")
		}
		time.Sleep(25 * time.Millisecond)
	}
	raw, _ := json.Marshal(sdk.ConversationSend{ClientMessageID: "disagreement-via-agent", Message: "Resolve disagreement fixture:\n" + d.ID})
	var run sdk.ConversationRun
	json.Unmarshal(b.call("POST", "/agent/conversations/"+sourceID+"/messages", string(raw), 202).Body.Bytes(), &run)
	run = waitPeerVerificationRun(t, b, "/agent/conversations/"+sourceID+"/runs/"+run.ID)
	var flow []string
	raisedStep, decidedStep, reads := -1, -1, 0
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status != "completed" {
				t.Fatalf("Agent disagreement tool failed: %+v", call)
			}
			if call.Name == "tool_result_read" {
				reads++
				continue
			}
			flow = append(flow, call.Name)
			if call.ID == "dispute-raise" {
				raisedStep = step.Number
			}
			if call.ID == "dispute-decide" {
				decidedStep = step.Number
			}
		}
	}
	if strings.Join(flow, ",") != "delegation_get,delegation_update,delegation_disagreement,delegation_update" || reads == 0 {
		t.Fatalf("Agent failed to read original evidence: flow=%v reads=%d final=%+v", flow, reads, run.Steps[len(run.Steps)-1])
	}
	unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), d)
	if d.Status != "delivered" || len(d.Disagreements) != 1 || d.Disagreements[0].Status != "resolved" {
		t.Fatalf("disagreement missing or automatically accepted: %+v", d.Disagreements)
	}
	var history sdk.ConversationDisagreementHistory
	json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID+"/disagreements/"+d.Disagreements[0].ID, "", 200).Body.Bytes(), &history)
	if len(history.Items) != 2 {
		t.Fatalf("lost immutable issue history: %+v", history)
	}
	decision, raised := history.Items[0], history.Items[1]
	if decision.DecisionActor == nil || decision.DecisionActor.AgentID != "default" || decision.DecisionActor.Source == nil || decision.DecisionActor.Source.RunID != run.ID || decision.DecisionActor.Source.BeforeStep != decidedStep+1 || raised.Actor.AgentID != "default" || raised.Actor.Source == nil || raised.Actor.Source.RunID != run.ID || raised.Actor.Source.BeforeStep != raisedStep+1 || len(raised.Claims[0].Receipts) != 1 || decision.Decision.AdoptClaimID != raised.Claims[0].ID {
		t.Fatalf("Agent issue identity or original evidence lost: %+v", history)
	}
	return history
}

func grantCollaborationPermissions(t *testing.T, host *Host, b *browser) {
	t.Helper()
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		kept := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) {
				kept = append(kept, p)
			}
		}
		for _, op := range sdk.ConversationCollaborationOperations() {
			kept = append(kept, identitysdk.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identitysdk.DataScopeOwner})
		}
		return kept
	})
}
