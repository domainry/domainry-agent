package web

import (
	"context"
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type peerWebModel struct {
	mu          sync.Mutex
	requests    []sdk.ConversationStepRequest
	collaborate bool
	modelKey    string
}

type peerReviewIssuerModel struct {
	peerWebModel
	disagreementWire map[string]*peerDisagreementWire
	pageMu           sync.Mutex
	resultPages      map[string]string
}

func (m *peerReviewIssuerModel) captureResultPage(page sdk.ConversationResultSlice) (string, int) {
	key := page.Reference.ConversationID + ":" + page.Reference.RunID + ":" + strconv.Itoa(page.Reference.Step) + ":" + page.Reference.CallID
	m.pageMu.Lock()
	defer m.pageMu.Unlock()
	if m.resultPages == nil {
		m.resultPages = map[string]string{}
	}
	current := m.resultPages[key]
	if page.Offset == len(current) {
		current += page.JSONText
		m.resultPages[key] = current
	}
	return current, len(current)
}

func (m *peerReviewIssuerModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	if result, handled := m.resolveFixtureDisagreementStep(in); handled {
		return result, nil
	}
	id, reviewed := "", false
	readJSON, nextOffset := "", 0
	var pending *sdk.ConversationResultReference
	var d sdk.ConversationDelegationDetail
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Verify fixture request:\n") {
			id = strings.TrimPrefix(message.Content, "Verify fixture request:\n")
		}
		if message.Role == "tool" {
			if message.ToolCallID == "issuer-review" {
				reviewed = true
			}
			if message.ToolCallID == "issuer-get" {
				var wire struct {
					sdk.ConversationToolResult
					Representation string                          `json:"representation"`
					Reference      sdk.ConversationResultReference `json:"reference"`
				}
				if json.Unmarshal([]byte(message.Content), &wire) == nil && wire.Representation == "stored_result_preview" {
					ref := wire.Reference
					pending = &ref
				} else if wire.Status == "completed" {
					_ = unmarshalPeerDetail(wire.Content, &d)
				}
			}
			if strings.HasPrefix(message.ToolCallID, "issuer-get-read-") {
				var result sdk.ConversationToolResult
				var page sdk.ConversationResultSlice
				var original sdk.ConversationToolResult
				if json.Unmarshal([]byte(message.Content), &result) == nil && result.Status == "completed" && json.Unmarshal(result.Content, &page) == nil {
					readJSON, nextOffset = m.captureResultPage(page)
					if page.Complete && json.Unmarshal([]byte(readJSON), &original) == nil {
						_ = unmarshalPeerDetail(original.Content, &d)
					}
				}
			}
		}
	}
	var call sdk.ConversationToolCall
	if id != "" && !reviewed && d.ID == "" && pending != nil {
		raw, _ := json.Marshal(sdk.ConversationResultRead{Reference: *pending, Offset: nextOffset, MaxBytes: 2048})
		call = sdk.ConversationToolCall{ID: "issuer-get-read-" + strconv.Itoa(nextOffset), Name: "tool_result_read", Arguments: string(raw)}
	} else if id != "" && !reviewed && d.ID == "" {
		call = sdk.ConversationToolCall{ID: "issuer-get", Name: "delegation_get", Arguments: `{"id":"` + id + `"}`}
	}
	if id != "" && d.ID != "" && !reviewed {
		raw, _ := json.Marshal(map[string]any{"id": id, "update": map[string]any{"expected_revision": d.Revision, "action": "review_delivery", "reason": "核对真实交付记录", "review": sdk.ConversationDeliveryReview{DeliveryDigest: d.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "已对照交付内容、工具回执和当前要求"}}}}})
		call = sdk.ConversationToolCall{ID: "issuer-review", Name: "delegation_update", Arguments: string(raw)}
	}
	if call.ID != "" {
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, nil
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

func (*peerWebModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{Content: "Ready"}, nil
}
func (m *peerWebModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	key := m.modelKey
	if key == "" {
		key = "peer"
	}
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: key, Fingerprint: key + "-v1"}
}

func (*peerWebModel) ConversationModelCapabilities() sdk.ConversationModelCapabilities {
	return sdk.ConversationModelCapabilities{ContextTokenLimit: 256_000, ImageInput: true, ProtocolContinuation: true}
}

func (*peerWebModel) ConversationModelDefaultReasoningEffort() string { return "" }

func peerPromptPlan(content string) *sdk.ConversationPlan {
	const marker = "Current durable execution plan (guidance and evidence, not authorization):\n"
	index := strings.Index(content, marker)
	if index < 0 {
		return nil
	}
	var plan sdk.ConversationPlan
	if json.NewDecoder(strings.NewReader(content[index+len(marker):])).Decode(&plan) != nil || plan.Version < 1 {
		return nil
	}
	return &plan
}

func peerPlanStepUpdates(steps []sdk.ConversationPlanStep) []sdk.ConversationPlanStepUpdate {
	out := make([]sdk.ConversationPlanStepUpdate, 0, len(steps))
	for _, step := range steps {
		out = append(out, sdk.ConversationPlanStepUpdate{
			ID: step.ID, Title: step.Title, Status: step.Status, DependsOn: append([]string{}, step.DependsOn...),
			Input: step.Input, ExpectedOutput: step.ExpectedOutput, RequirementFields: append([]string{}, step.RequirementFields...),
			Evidence: append([]sdk.ConversationResultReference{}, step.Evidence...), Artifacts: append([]sdk.ConversationArtifactReference{}, step.Artifacts...),
			Outcome: step.Outcome, Blocker: step.Blocker,
		})
	}
	return out
}

func (m *peerWebModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, in)
	m.mu.Unlock()
	if m.collaborate {
		id := ""
		var d sdk.ConversationDelegationDetail
		planned, resumePlanned, planCompleted, sent, delivered, timed := false, false, false, false, false, false
		var promptPlan, currentPlan *sdk.ConversationPlan
		currentAgreementRevision := int64(1)
		var timeRef, deliveryRef *sdk.ConversationResultReference
		for _, message := range in.Messages {
			if plan := peerPromptPlan(message.Content); plan != nil {
				promptPlan, currentPlan = plan, plan
			}
			if match := regexp.MustCompile(`Accepted agreement revision: ([0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
				if revision, err := strconv.ParseInt(match[1], 10, 64); err == nil && revision > 0 {
					currentAgreementRevision = revision
				}
			}
			if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
				id = match[1]
			}
			if message.Role == "tool" {
				var result sdk.ConversationToolResult
				_ = json.Unmarshal([]byte(message.Content), &result)
				if result.Status == "completed" {
					switch message.ToolCallID {
					case "peer-plan":
						planned = true
					case "peer-plan-complete":
						planCompleted = true
					case "peer-plan-resume":
						planned, resumePlanned = true, true
						var updated struct {
							Plan sdk.ConversationPlan `json:"plan"`
						}
						if json.Unmarshal(result.Content, &updated) == nil && updated.Plan.Version > 0 {
							currentPlan = &updated.Plan
						}
					case "peer-plan-resume-complete":
						planCompleted = true
					case "peer-time":
						timed = true
						// Read only wire-visible JSON; real providers do not serialize internal message metadata.
						var wire struct {
							Reference *sdk.ConversationResultReference `json:"reference"`
						}
						_ = json.Unmarshal([]byte(message.Content), &wire)
						timeRef = wire.Reference
					case "peer-get":
						_ = unmarshalPeerDetail(result.Content, &d)
					case "peer-send":
						sent = true
					case "peer-deliver":
						delivered = true
						var wire struct {
							Reference *sdk.ConversationResultReference `json:"reference"`
						}
						_ = json.Unmarshal([]byte(message.Content), &wire)
						deliveryRef = wire.Reference
					}
				}
			}
		}
		if id != "" {
			call := sdk.ConversationToolCall{}
			if promptPlan != nil && !resumePlanned {
				steps := peerPlanStepUpdates(promptPlan.Steps)
				dependsOn := []string{}
				for index := len(promptPlan.Steps) - 1; index >= 0; index-- {
					if promptPlan.Steps[index].Status == sdk.ConversationPlanStepCompleted {
						dependsOn = []string{promptPlan.Steps[index].ID}
						break
					}
				}
				steps = append(steps, sdk.ConversationPlanStepUpdate{ID: "review-updated-requirements-v" + strconv.FormatInt(promptPlan.Version, 10), Title: "复核变更后的委派要求", DependsOn: dependsOn, Input: "当前依赖要求与既有完成证据", ExpectedOutput: "按最新要求重新提交的核查交付", Status: sdk.ConversationPlanStepInProgress, RequirementFields: []string{"dependencies", "deliverable"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}})
				raw, _ := json.Marshal(sdk.ConversationPlanUpdate{ClientID: "peer-plan-resume-v" + strconv.FormatInt(promptPlan.Version, 10), ExpectedVersion: promptPlan.Version, AgreementRevision: currentAgreementRevision, Reason: "依赖要求已变化，保留既有完成证据并复核当前要求", Steps: steps})
				call = sdk.ConversationToolCall{ID: "peer-plan-resume", Name: "plan_update", Arguments: string(raw)}
			} else if !planned {
				steps := []sdk.ConversationPlanStepUpdate{
					{ID: "inspect-time", Title: "读取并核对当前时间", DependsOn: []string{}, Input: "当前委派要求", ExpectedOutput: "可追溯的时间回执", Status: sdk.ConversationPlanStepInProgress, RequirementFields: []string{"completion_conditions"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}},
					{ID: "publish-review", Title: "提交核查结论", DependsOn: []string{"inspect-time"}, Input: "已核对的时间回执", ExpectedOutput: "符合约定的核查交付", Status: sdk.ConversationPlanStepPending, RequirementFields: []string{"deliverable"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}},
				}
				raw, _ := json.Marshal(sdk.ConversationPlanUpdate{ClientID: "peer-plan-v1", ExpectedVersion: 0, AgreementRevision: 1, Reason: "先取得原始回执，再提交可复核交付", Steps: steps})
				call = sdk.ConversationToolCall{ID: "peer-plan", Name: "plan_update", Arguments: string(raw)}
			} else if promptPlan == nil && !timed {
				call = sdk.ConversationToolCall{ID: "peer-time", Name: "time_now", Arguments: `{}`}
			} else if d.ID == "" {
				call = sdk.ConversationToolCall{ID: "peer-get", Name: "delegation_get", Arguments: `{"id":"` + id + `"}`}
			} else if promptPlan == nil && !sent {
				raw, _ := json.Marshal(map[string]any{"id": id, "message": map[string]any{"to_agent_id": d.FromAgentID, "brief_version": d.Brief.Version, "agreement_revision": d.AgreementRevision, "content": "核查口径已确认，请按当前版本验收。"}})
				call = sdk.ConversationToolCall{ID: "peer-send", Name: "agent_message", Arguments: string(raw)}
			} else if !delivered {
				conditions := []sdk.ConversationConditionAssessment{}
				if timeRef != nil {
					for _, rule := range d.Brief.VerificationRules {
						if rule.Kind == "receipt" && rule.Tool == "time_now" {
							conditions = append(conditions, sdk.ConversationConditionAssessment{Condition: rule.Condition, Verdict: "met", Basis: "真实时钟回执", Receipts: []sdk.ConversationResultReference{*timeRef}})
						}
					}
				}
				raw, _ := json.Marshal(map[string]any{"id": id, "update": map[string]any{"expected_revision": d.Revision, "action": "deliver", "reason": "已核对约定条件", "delivery": map[string]any{"brief_version": d.Brief.Version, "agreement_revision": d.AgreementRevision, "summary": "独立核查完成", "conditions": conditions, "data": map[string]any{"verified": true}, "evidence": []any{}, "unresolved": []string{}}}})
				call = sdk.ConversationToolCall{ID: "peer-deliver", Name: "delegation_update", Arguments: string(raw)}
			} else if !planCompleted && deliveryRef != nil && (promptPlan != nil || timeRef != nil) {
				if promptPlan != nil && currentPlan != nil {
					steps := peerPlanStepUpdates(currentPlan.Steps)
					resumeStepID := "review-updated-requirements-v" + strconv.FormatInt(promptPlan.Version, 10)
					resumeEvidence := []sdk.ConversationResultReference{*deliveryRef}
					if timeRef != nil {
						resumeEvidence = append([]sdk.ConversationResultReference{*timeRef}, resumeEvidence...)
					}
					for index := range steps {
						if steps[index].ID == resumeStepID {
							steps[index].Status = sdk.ConversationPlanStepCompleted
							steps[index].Outcome = "最新依赖要求已经复核并重新提交"
							steps[index].Blocker = ""
							steps[index].Evidence = resumeEvidence
						}
					}
					raw, _ := json.Marshal(sdk.ConversationPlanUpdate{ClientID: "peer-plan-resume-complete-v" + strconv.FormatInt(currentPlan.Version, 10), ExpectedVersion: currentPlan.Version, AgreementRevision: currentPlan.AgreementRevision, Reason: "最新要求、原始回执和新交付回执已经完成恢复步骤", Steps: steps})
					call = sdk.ConversationToolCall{ID: "peer-plan-resume-complete", Name: "plan_update", Arguments: string(raw)}
				} else {
					steps := []sdk.ConversationPlanStepUpdate{
						{ID: "inspect-time", Title: "读取并核对当前时间", DependsOn: []string{}, Input: "当前委派要求", ExpectedOutput: "可追溯的时间回执", Status: sdk.ConversationPlanStepCompleted, RequirementFields: []string{"completion_conditions"}, Outcome: "当前时间回执已经核对", Evidence: []sdk.ConversationResultReference{*timeRef}, Artifacts: []sdk.ConversationArtifactReference{}},
						{ID: "publish-review", Title: "提交核查结论", DependsOn: []string{"inspect-time"}, Input: "已核对的时间回执", ExpectedOutput: "符合约定的核查交付", Status: sdk.ConversationPlanStepCompleted, RequirementFields: []string{"deliverable"}, Outcome: "核查结论已经提交", Evidence: []sdk.ConversationResultReference{*deliveryRef}, Artifacts: []sdk.ConversationArtifactReference{}},
					}
					raw, _ := json.Marshal(sdk.ConversationPlanUpdate{ClientID: "peer-plan-v2", ExpectedVersion: 1, AgreementRevision: 1, Reason: "原始回执和交付回执已经完成全部步骤", Steps: steps})
					call = sdk.ConversationToolCall{ID: "peer-plan-complete", Name: "plan_update", Arguments: string(raw)}
				}
			}
			if call.ID != "" {
				return sdk.ConversationStepResult{Usage: map[string]any{"input_tokens": 100, "output_tokens": 20}, FinishReason: "tool_calls", Model: "peer", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, nil
			}
		}
	}
	return sdk.ConversationStepResult{Usage: map[string]any{"input_tokens": 100, "output_tokens": 20}, FinishReason: "stop", Model: "peer", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "已完成独立核查，等待提交验收。"}}, nil
}
func TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery(t *testing.T) {
	const initial, changed = "Peer-Initial!22", "Peer-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "peer-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "peer-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &peerWebModel{collaborate: true, modelKey: "review"}
	registry := map[string]sdk.ConversationModel{"review": model}
	definitions := []sdk.AgentSchema{{Key: "reviewer", Version: "1", Name: "核查能力", Instructions: "独立核查并说明证据", Tools: []string{"time_now", "delegation_get", "agent_message", "delegation_update"}}}
	prices := map[string]sdk.ConversationModelPrice{"review": {Currency: "CNY", InputPerMillion: 2, OutputPerMillion: 4, Basis: "fixture configured tariff", UpdatedAt: time.Now().UTC()}}
	frozenPrice := prices["review"]

	options := Options{DatabasePath: filepath.Join(t.TempDir(), "peer.db"), RuntimeID: "peer-runtime", WorkspaceID: "peer-workspace", ApplicationKey: "peer-app", Agent: agentmodule.Options{ConversationProvider: &peerReviewIssuerModel{}, ConversationOptions: agentmodule.ConversationOptions{ContextBytes: 131072, AgentModels: registry, AgentDefinitions: definitions, AgentModelPrices: prices, Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	definitions[0].Instructions = "caller mutation must not reach runtime"
	prices["review"] = sdk.ConversationModelPrice{}
	registry["review"] = nil // Host-side map mutations must not change accepted execution configuration.
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	var page sdk.ConversationAgentPage
	json.Unmarshal(b.call("GET", "/agent/agents", "", 200).Body.Bytes(), &page)
	if len(page.Items) < 1 || len(page.Tools) == 0 {
		t.Fatalf("directory %+v", page)
	}
	var agent sdk.ConversationAgent
	json.Unmarshal(b.call("POST", "/agent/agents", `{"client_id":"reviewer","definition_key":"reviewer","expected_revision":0,"name":"核查 Agent","description":"核查数字","instructions":"独立核查并说明证据","tools":["time_now","delegation_get","agent_message","delegation_update"],"skill_keys":[],"model_key":"review","enabled":true,"max_concurrent":1}`, 200).Body.Bytes(), &agent)
	if agent.ID == "" || agent.DefinitionKey != "reviewer" || agent.DefinitionVersion != "1" || agent.Instructions != "独立核查并说明证据" {
		t.Fatal("Agent not created")
	}
	var independent sdk.ConversationAgent
	if err = json.Unmarshal(b.call("POST", "/agent/agents", `{"client_id":"second-reviewer","definition_key":"reviewer","name":"第二核查 Agent","model_key":"review","enabled":false,"max_concurrent":1}`, 200).Body.Bytes(), &independent); err != nil {
		t.Fatal(err)
	}
	if independent.ID == agent.ID || independent.DefinitionKey != agent.DefinitionKey || independent.Instructions != agent.Instructions || independent.Enabled {
		t.Fatalf("definition and instance identities collapsed: %+v %+v", agent, independent)
	}
	var source sdk.Conversation
	json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"peer-source","title":"总任务"}`, 200).Body.Bytes(), &source)
	input := sdk.ConversationDelegationCreate{ClientID: "peer-work", ConversationID: source.ID, AgentID: agent.ID, Requirements: sdk.ConversationAgentRequirements{TaskType: "publish_review", Tools: []string{"time_now"}}, Purpose: "独立核查", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核查发布说明", Deliverable: "核查结论", CompletionConditions: []string{"说明依据", "验证结果为 true", "读取实际时间"}, VerificationRules: []sdk.ConversationCompletionRule{{Condition: 1, Kind: "data", Schema: json.RawMessage(`{"type":"object","properties":{"verified":{"const":true}},"required":["verified"]}`)}, {Condition: 2, Kind: "receipt", Tool: "time_now", ResultSchema: json.RawMessage(`{"type":"object"}`)}}}, Input: "仅使用提供的发布说明", OutputSchema: json.RawMessage(`{"type":"object","properties":{"verified":{"type":"boolean"}},"required":["verified"],"additionalProperties":false}`)}
	input.StructuredInput = &sdk.ConversationStructuredInput{Schema: json.RawMessage(`{"type":"object","properties":{"currency":{"type":"string"}},"required":["currency"],"additionalProperties":false}`), Data: json.RawMessage(`{"currency":"EUR"}`)}
	invalidStructured := input
	invalidStructured.ClientID = "invalid-typed-input"
	invalidStructured.StructuredInput = &sdk.ConversationStructuredInput{Schema: input.StructuredInput.Schema, Data: json.RawMessage(`{"currency":123}`)}
	invalidInputRaw, _ := json.Marshal(invalidStructured)
	b.call("POST", "/agent/delegations", string(invalidInputRaw), 400)
	var beforeWork sdk.ConversationDelegationPage
	json.Unmarshal(b.call("GET", "/agent/delegations", "", 200).Body.Bytes(), &beforeWork)
	if len(beforeWork.Items) != 0 {
		t.Fatal("invalid input admitted work")
	}
	raw, _ := json.Marshal(input)
	var d sdk.ConversationDelegationDetail
	unmarshalPeerDetail(b.call("POST", "/agent/delegations", string(raw), 200).Body.Bytes(), &d)
	if d.ID == "" || d.ConversationID == source.ID {
		t.Fatalf("delegation %+v", d)
	}
	approved := map[string]bool{}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &d)
		if d.Task != nil && d.Task.Interaction != nil && d.Task.Interaction.Status == "pending" && !approved[d.Task.Interaction.ID] {
			i := d.Task.Interaction
			answer, _ := json.Marshal(sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"})
			b.call("POST", "/agent/conversations/"+d.ConversationID+"/runs/"+i.RunID+"/respond", string(answer), 200)
			approved[i.ID] = true
		}
		if d.Task != nil && d.Task.Status == "completed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if d.Task == nil || d.Task.Status != "completed" || d.Status != "delivered" || d.Task.Plan == nil || d.Task.Plan.Version != 2 || d.Task.Plan.Steps[0].Status != sdk.ConversationPlanStepCompleted || d.Task.Plan.Steps[1].Status != sdk.ConversationPlanStepCompleted {
		t.Fatalf("execution %+v", d)
	}
	if d.Task.Work == nil || d.Task.Work.Allocation.Usage.ModelCalls != 7 || d.Task.Work.Allocation.Usage.ToolCalls != 6 || d.Task.Work.Allocation.Usage.InputTokens != 700 || d.Task.Work.Allocation.Usage.OutputTokens != 140 || !d.Task.Work.Allocation.Usage.CostKnown || d.Task.Work.Allocation.Usage.Currency != "CNY" || d.Task.Work.TotalUsage.ModelCalls != d.Task.Work.Allocation.Usage.ModelCalls || d.Task.Work.TotalUsage.ToolCalls != d.Task.Work.Allocation.Usage.ToolCalls || d.Task.Work.TotalUsage.InputTokens != d.Task.Work.Allocation.Usage.InputTokens || d.Task.Work.TotalUsage.OutputTokens != d.Task.Work.Allocation.Usage.OutputTokens || d.Task.Work.TotalUsage.ModelCost != d.Task.Work.Allocation.Usage.ModelCost || d.Task.Work.TotalUsage.Currency != d.Task.Work.Allocation.Usage.Currency {
		t.Fatalf("whole-work accounting %+v", d.Task.Work)
	}
	if d.Task.Progress.ModelCalls != 7 || d.Task.Progress.ToolCalls != 6 || d.Task.Progress.InputTokens != 700 || d.Task.Progress.OutputTokens != 140 || d.Task.Diagnostic.State != "blocked" || d.Task.Diagnostic.Action != "report_blocker" || len(d.Task.Diagnostic.Reasons) != 1 || d.Task.Diagnostic.Reasons[0] != "blocker:delivery_review_required" || d.Task.Diagnostic.StageOutcomes != 2 || d.Task.Diagnostic.RemainingStages != 0 {
		t.Fatalf("task progress diagnostic progress=%+v diagnostic=%+v", d.Task.Progress, d.Task.Diagnostic)
	}
	model.mu.Lock()
	requests := append([]sdk.ConversationStepRequest{}, model.requests...)
	model.mu.Unlock()
	structured := false
	isolated := false
	for _, in := range requests {
		for _, message := range in.Messages {
			if strings.Contains(message.Content, `"currency":"EUR"`) {
				structured = true
			}
			if strings.Contains(message.Content, "独立核查并说明证据") {
				isolated = true
				if in.ModelIdentity.Model != "review" {
					t.Fatalf("wrong selected model: %+v", in.ModelIdentity)
				}
				keys := make([]string, len(in.Tools))
				for index := range in.Tools {
					keys[index] = in.Tools[index].Key
				}
				if strings.Join(keys, ",") != "agent_message,delegation_get,delegation_update,time_now,plan_update" {
					t.Fatalf("wrong Agent tools %v", keys)
				}
			}
		}
	}
	if !structured {
		t.Fatal("validated structured input did not reach model")
	}
	if !isolated {
		t.Fatal("Agent instructions not executed")
	}
	if len(approved) != 0 {
		t.Fatalf("accepted peer communication unnecessarily waited for user approval: %+v", approved)
	}
	actualMessage := false
	for _, m := range d.Messages {
		if m.Kind == "message" && m.FromAgentID == agent.ID && m.ToAgentID == "default" {
			actualMessage = true
		}
	}
	if !actualMessage {
		t.Fatalf("peer message missing %+v", d.Messages)
	}
	if d.Verification == nil || len(d.Verification.Checks) != 3 || d.Verification.Ready || d.Verification.Checks[1].Verdict != "met" || d.Verification.Checks[2].Verdict != "met" || len(d.Verification.Checks[2].Receipts) != 1 {
		t.Fatalf("wire-visible original receipts did not reach program verification: %+v", d.Verification)
	}
	originalConditions := d.Delivery.Conditions
	update := sdk.ConversationDelegationUpdate{ClientID: "bad-delivery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "已检查", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, Summary: "核查完成", Data: json.RawMessage(`{"verified":"yes"}`)}}
	raw, _ = json.Marshal(update)
	b.call("POST", "/agent/delegations/"+d.ID+"/decisions", string(raw), 400)
	// The worker may finish between the delegation and task projections.
	// Start this distinct user mutation from a fresh complete snapshot.
	unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &d)
	update.ExpectedRevision = d.Revision
	update.ClientID = "delivery"
	update.Delivery.Data = json.RawMessage(`{"verified":true}`)
	update.Delivery.Conditions = originalConditions
	update.Delivery.Evidence = []sdk.ConversationRunReference{{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}}
	raw, _ = json.Marshal(update)
	unmarshalPeerDetail(b.call("POST", "/agent/delegations/"+d.ID+"/decisions", string(raw), 200).Body.Bytes(), &d)
	matchRequest := sdk.ConversationAgentMatchRequest{ConversationID: source.ID, Requirements: sdk.ConversationAgentRequirements{TaskType: "publish_review", Tools: []string{"time_now"}}}
	matchRaw, _ := json.Marshal(matchRequest)
	var matches sdk.ConversationAgentMatchPage
	if err = json.Unmarshal(b.call("POST", "/agent/agents/matches", string(matchRaw), 200).Body.Bytes(), &matches); err != nil {
		t.Fatal(err)
	}
	if matches.RecommendedAgentID != agent.ID {
		t.Fatalf("wrong recommendation %+v", matches)
	}
	foundSample := false
	for _, candidate := range matches.Items {
		if candidate.AgentID == agent.ID {
			foundSample = candidate.History.Runs == 1 && candidate.History.CompletedRuns == 1 && candidate.History.AcceptedDeliveries == 0 && candidate.Cost.Known && candidate.Cost.InputTokens == 700 && candidate.Cost.OutputTokens == 140 && candidate.Cost.Currency == "CNY"
		}
	}
	if !foundSample {
		t.Fatalf("actual execution was not observed: %+v", matches)
	}
	invalidInput := input
	invalidInput.ClientID = "unfit-work"
	invalidInput.Requirements.Tools = []string{"calculate"}
	invalidRaw, _ := json.Marshal(invalidInput)
	b.call("POST", "/agent/delegations", string(invalidRaw), 409)
	// The issuer reads current evidence through real tools and saves an Agent
	// review without turning it into user authorization or accepting the task.
	deadline = time.Now().Add(15 * time.Second)
	for {
		var current sdk.Conversation
		json.Unmarshal(b.call("GET", "/agent/conversations/"+source.ID, "", 200).Body.Bytes(), &current)
		if current.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("issuer notification did not finish")
		}
		time.Sleep(25 * time.Millisecond)
	}
	send, _ := json.Marshal(sdk.ConversationSend{ClientMessageID: "review-via-agent", Message: "Verify fixture request:\n" + d.ID})
	var issuerRun sdk.ConversationRun
	json.Unmarshal(b.call("POST", "/agent/conversations/"+source.ID+"/messages", string(send), 202).Body.Bytes(), &issuerRun)
	issuerRun = waitPeerVerificationRun(t, b, "/agent/conversations/"+source.ID+"/runs/"+issuerRun.ID)
	unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &d)
	if d.Status != "delivered" || !d.Verification.Ready || d.Verification.AgentID != "default" || d.Verification.Source == nil || d.Verification.Source.RunID != issuerRun.ID || d.Verification.Source.BeforeStep != len(issuerRun.Steps)-1 || d.Verification.Checks[0].Method != "agent" || d.Task.Status != "completed" {
		raw, _ := json.Marshal(issuerRun)
		t.Fatalf("issuer review lost provenance or changed acceptance: report=%+v run=%s", d.Verification, raw)
	}
	reviewedByTool := false
	for _, step := range issuerRun.Steps {
		for _, call := range step.Calls {
			reviewedByTool = reviewedByTool || call.ID == "issuer-review" && call.Name == "delegation_update" && call.Status == "completed"
		}
	}
	if !reviewedByTool {
		t.Fatalf("issuer review tool failed %+v", issuerRun)
	}
	disagreementHistory := verifyPeerDisagreementAgent(t, b, source.ID, &d)
	changedAgent := sdk.ConversationAgentWrite{ClientID: "config-change", ExpectedRevision: agent.Revision, Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions + " New configuration.", Tools: agent.Tools, SkillKeys: agent.SkillKeys, ModelKey: agent.ModelKey, Enabled: true, MaxConcurrent: agent.MaxConcurrent}
	changeRaw, _ := json.Marshal(changedAgent)
	b.call("PUT", "/agent/agents/"+agent.ID, string(changeRaw), 200)
	messageRaw, _ := json.Marshal(sdk.ConversationAgentMessageSend{ClientID: "stale-wakeup", ToAgentID: agent.ID, Content: "Continue the accepted task", BriefVersion: d.Brief.Version})
	blocked := b.call("POST", "/agent/delegations/"+d.ID+"/messages", string(messageRaw), 409)
	if !strings.Contains(blocked.Body.String(), "agent_changed") {
		t.Fatalf("message changed frozen configuration: %s", blocked.Body.String())
	}
	update = sdk.ConversationDelegationUpdate{ClientID: "accept", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "完成条件逐项核对通过", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: d.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "已核对实际交付与当前完成条件"}}}}
	raw, _ = json.Marshal(update)
	response := b.call("POST", "/agent/delegations/"+d.ID+"/decisions", string(raw), 200)
	unmarshalPeerDetail(response.Body.Bytes(), &d)
	if d.Status != "accepted_delivery" {
		t.Fatalf("acceptance %s", response.Body.String())
	}
	b.call("POST", "/agent/delegations/"+d.ID+"/decisions", string(raw), 200)
	var deliveryHistory sdk.ConversationDeliveryHistory
	if err = json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID+"/deliveries", "", 200).Body.Bytes(), &deliveryHistory); err != nil || len(deliveryHistory.Items) != 4 || deliveryHistory.Items[0].Kind != "accept_delivery" || deliveryHistory.Items[1].Verification.Source == nil || deliveryHistory.Items[1].Verification.Source.RunID != issuerRun.ID || deliveryHistory.Items[3].Verification.Ready {
		t.Fatalf("immutable delivery history %+v %v", deliveryHistory, err)
	}
	if os.Getenv("AGENT_PEER_BROWSER") == "1" {
		project, err := filepath.Abs("../../..")
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(nil)
		origin := "http://" + server.Listener.Addr().String()
		ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
		if err != nil {
			t.Fatal(err)
		}
		server.Config.Handler = ui
		server.Start()
		defer server.Close()
		command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/collaboration.browser.mjs"))
		command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+source.ID)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err = command.Run(); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("AGENT_E05_BROWSER") == "1" {
		project, err := filepath.Abs("../../..")
		if err != nil {
			t.Fatal(err)
		}
		output := os.Getenv("AGENT_UI_TEST_OUTPUT")
		if output == "" || os.Getenv("AGENT_NODE_BINARY") == "" {
			t.Fatal("AGENT_UI_TEST_OUTPUT and AGENT_NODE_BINARY are required")
		}
		server := httptest.NewUnstartedServer(nil)
		origin := "http://" + server.Listener.Addr().String()
		ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
		if err != nil {
			t.Fatal(err)
		}
		server.Config.Handler = ui
		server.Start()
		defer server.Close()
		command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/work-diagnostics.browser.mjs"))
		command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+output, "AGENT_UI_CONVERSATION="+source.ID)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err = command.Run(); err != nil {
			t.Fatal(err)
		}
	}
	// Reopen the same SQLite database with fresh services and authorization.
	// Restore the caller-owned config maps deliberately mutated earlier.
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	options.Agent.ConversationOptions.AgentModels = map[string]sdk.ConversationModel{"review": model}
	options.Agent.ConversationOptions.AgentModelPrices = map[string]sdk.ConversationModelPrice{"review": frozenPrice}
	options.Agent.ConversationOptions.AgentDefinitions = []sdk.AgentSchema{{Key: "reviewer", Version: "1", Name: "核查能力", Instructions: "独立核查并说明证据", Tools: []string{"time_now", "delegation_get", "agent_message", "delegation_update"}}}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b.handler = handler
	b.login("admin@example.com", changed)
	var restored sdk.ConversationDeliveryHistory
	json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID+"/deliveries", "", 200).Body.Bytes(), &restored)
	beforeJSON, _ := json.Marshal(deliveryHistory)
	afterJSON, _ := json.Marshal(restored)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("restart changed immutable verification history: %s", afterJSON)
	}
	var restoredDelegation sdk.ConversationDelegationDetail
	json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &restoredDelegation)
	if restoredDelegation.Status != "accepted_delivery" || restoredDelegation.Verification == nil || restoredDelegation.Verification.DeliveryDigest != d.Verification.DeliveryDigest {
		t.Fatalf("restart lost acceptance: %+v", restoredDelegation)
	}
	var restoredDisagreements sdk.ConversationDisagreementHistory
	json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID+"/disagreements/"+d.Disagreements[0].ID, "", 200).Body.Bytes(), &restoredDisagreements)
	originalIssues, _ := json.Marshal(disagreementHistory)
	restoredIssues, _ := json.Marshal(restoredDisagreements)
	if string(originalIssues) != string(restoredIssues) {
		t.Fatal("restart changed Agent disagreement history")
	}
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, permission := range previous {
			if permission.PermissionKey != sdk.ConversationToolActionPrefix+"time_now" {
				out = append(out, permission)
			}
		}
		return out
	})
	matches = sdk.ConversationAgentMatchPage{}
	if err = json.Unmarshal(b.call("POST", "/agent/agents/matches", string(matchRaw), 200).Body.Bytes(), &matches); err != nil {
		t.Fatal(err)
	}
	if matches.RecommendedAgentID != "" {
		t.Fatalf("revoked peer recommended: %+v", matches)
	}
	var revoked sdk.ConversationDelegationDetail
	if err = json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &revoked); err != nil {
		t.Fatal(err)
	}
	for _, message := range revoked.Messages {
		if message.Kind == "message" && strings.Contains(message.Content, "核查口径已确认") {
			t.Fatalf("revoked source leaked through peer message: %+v source=%+v", message, message.Source)
		}
	}
	if len(revoked.Disagreements) != 0 || !revoked.DisagreementsOmitted {
		t.Fatal("revoked disagreement sources leaked", revoked.Disagreements)
	}
	b.call("GET", "/agent/delegations/"+d.ID+"/disagreements/"+d.Disagreements[0].ID, "", 403)
	if revoked.Task == nil || revoked.Task.Result != nil {
		t.Fatalf("revoked raw execution result leaked %+v", revoked.Task)
	}
	// The attested public clock receipt has an independent reading policy.
	// Losing its execution grant hides raw execution but retains the submitted
	// projection and its historical versions under current delivery_read.
	wantDelivery, _ := json.Marshal(restoredDelegation.Delivery)
	actualDelivery, _ := json.Marshal(revoked.Delivery)
	if revoked.Delivery == nil || revoked.Verification == nil || !revoked.Verification.Ready || string(wantDelivery) != string(actualDelivery) {
		t.Fatalf("execution revocation altered independently readable submitted clock results: %s", actualDelivery)
	}
	var readableHistory sdk.ConversationDeliveryHistory
	if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID+"/deliveries", "", 200).Body.Bytes(), &readableHistory); err != nil {
		t.Fatal(err)
	}
	readableHistoryJSON, _ := json.Marshal(readableHistory)
	if string(afterJSON) != string(readableHistoryJSON) {
		t.Fatalf("execution revocation altered independently readable delivery history: %s", readableHistoryJSON)
	}
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, permission := range previous {
			if permission.PermissionKey != sdk.ConversationCollaborationPermission("delivery_read").Key {
				out = append(out, permission)
			}
		}
		return out
	})
	var deniedDelivery sdk.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &deniedDelivery); err != nil || deniedDelivery.Delivery != nil || deniedDelivery.Verification != nil {
		t.Fatal("current submitted values survived delivery reading revocation", deniedDelivery.Delivery, err)
	}
	b.call("GET", "/agent/delegations/"+d.ID+"/deliveries", "", 403)
}

// HTTP objects are complete snapshots; optional fields must not merge with
// the previous response when a review changes from Agent to user provenance.
func unmarshalPeerDetail(raw []byte, out *sdk.ConversationDelegationDetail) error {
	var current sdk.ConversationDelegationDetail
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	*out = current
	return nil
}

func waitPeerVerificationRun(t *testing.T, b *browser, path string) sdk.ConversationRun {
	t.Helper()
	// The Identity/source checks traverse both Agents' recorded work and page
	// large immutable results. Whole-package runs can exceed 90 seconds here;
	// this bound remains below the production run's 300-second budget.
	var current sdk.ConversationRun
	for deadline := time.Now().Add(180 * time.Second); time.Now().Before(deadline); {
		current = sdk.ConversationRun{}
		if err := json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if current.Status == "completed" {
			return current
		}
		if current.Terminal() || current.Status == "waiting_confirmation" {
			t.Fatalf("unexpected issuer review state %s/%s run=%+v", current.Status, current.ErrorCode, current)
		}
		time.Sleep(25 * time.Millisecond)
	}
	raw, _ := json.Marshal(current)
	t.Fatalf("issuer review did not finish: %s", raw)
	return current
}
