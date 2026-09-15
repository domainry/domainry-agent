package product

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-knowledge/contract"
)

type recordDeliveryModel struct{}

func (recordDeliveryModel) GenerateConversation(context.Context, agent.ConversationModelRequest) (agent.ConversationModelResult, error) {
	return agent.ConversationModelResult{Content: "Ready"}, nil
}
func (recordDeliveryModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "record-delivery", Fingerprint: "record-delivery-v1"}
}
func (recordDeliveryModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, key string, args any) (agent.ConversationStepResult, error) {
		raw, err := json.Marshal(args)
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: key, Arguments: string(raw)}}}}, err
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "Submitted the exact original structured record receipts."}}, nil
	}
	target, id := "", ""
	dispatched, delivered := false, false
	refs := map[string]agent.ConversationResultReference{}
	outputs := map[string]json.RawMessage{}
	var detail agent.ConversationDelegationDetail
	for _, m := range in.Messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "Delegate record request:\n") {
			target = strings.TrimPrefix(m.Content, "Delegate record request:\n")
		}
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(m.Content); len(match) > 1 {
			id = match[1]
		}
		if m.Role != "tool" {
			continue
		}
		var wire struct {
			agent.ConversationToolResult
			Reference *agent.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(m.Content), &wire) != nil || wire.Status != "completed" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual record tool failed: %s", m.ToolCallID)
		}
		dispatched = dispatched || m.ToolCallID == "record-dispatch"
		delivered = delivered || m.ToolCallID == "record-deliver"
		outputs[m.ToolCallID] = wire.Content
		if wire.Reference != nil {
			refs[m.ToolCallID] = *wire.Reference
		}
		if m.ToolCallID == "record-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
	}
	if target != "" && id == "" {
		if dispatched {
			return stop()
		}
		brief := agent.ConversationTaskBrief{Version: 1, Goal: "Create and verify one immutable personal structured record", Deliverable: "Exact original save, read and list receipts", Constraints: []string{}, Assumptions: []string{}, CompletionConditions: []string{"Create the original record", "Read its exact saved version", "Find it in the original record list"}}
		for i, key := range []string{"requirements_save", "requirements_read", "requirements_list"} {
			brief.VerificationRules = append(brief.VerificationRules, agent.ConversationCompletionRule{Condition: i, Kind: "receipt", Tool: key})
		}
		return tool("record-dispatch", "agent_delegate", map[string]any{"agent_id": target, "purpose": "Verify an independent Agent's actual structured record receipts", "brief": brief, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 60}, "input": "Create Delegated original with amount 9007199254740993, read its exact immutable revision and list it. Deliver all three original receipts.", "requirements": agent.ConversationAgentRequirements{Tools: []string{"requirements_save", "requirements_read", "requirements_list"}}})
	}
	if id == "" || delivered {
		return stop()
	}
	if _, ok := refs["record-save"]; !ok {
		return tool("record-save", "requirements_save", map[string]any{"expected_revision": 0, "title": "Delegated original", "status": "draft", "data": json.RawMessage(`{"amount":9007199254740993}`)})
	}
	var record contract.Record
	if json.Unmarshal(outputs["record-save"], &record) != nil || record.ID == "" || record.Revision != 1 || record.Title != "Delegated original" || string(record.Data) != `{"amount":9007199254740993}` {
		return agent.ConversationStepResult{}, fmt.Errorf("actual delegated original record changed")
	}
	if _, ok := refs["record-read"]; !ok {
		return tool("record-read", "requirements_read", map[string]any{"id": record.ID, "revision": record.Revision})
	}
	var read contract.Record
	if json.Unmarshal(outputs["record-read"], &read) != nil || read.ID != record.ID || read.Revision != record.Revision || string(read.Data) != string(record.Data) {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original version was not read")
	}
	if _, ok := refs["record-list"]; !ok {
		return tool("record-list", "requirements_list", map[string]any{"query": "Delegated original", "limit": 1})
	}
	var page contract.Page
	if json.Unmarshal(outputs["record-list"], &page) != nil || len(page.Items) != 1 || page.Items[0].ID != record.ID || string(page.Items[0].Data) != string(record.Data) {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original record list changed")
	}
	if detail.ID == "" {
		return tool("record-agreement", "delegation_get", map[string]string{"id": id})
	}
	conditions := []agent.ConversationConditionAssessment{}
	for i, callID := range []string{"record-save", "record-read", "record-list"} {
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Read the source-owned exact original structured record", Receipts: []agent.ConversationResultReference{refs[callID]}})
	}
	return tool("record-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Verified actual saved record, immutable version and list", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Original personal structured record receipts", Data: record.Data, Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}
