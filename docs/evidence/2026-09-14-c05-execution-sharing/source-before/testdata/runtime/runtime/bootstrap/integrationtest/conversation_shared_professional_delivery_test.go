package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
)

// The model fixture consumes only public wire results, including server-issued
// receipt references. Identity, Runtime, Report, Tools and the durable worker
// remain real; no proof or published source grant is fabricated by this model.
type sharedProfessionalDeliveryModel struct{ businessWebModel }

func sharedProfessionalJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (sharedProfessionalDeliveryModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, name string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: name, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "已处理当前委派，等待账号核对交付。"}}, nil
	}
	target, id, dispatched, delivered := "", "", false, false
	var detail agent.ConversationDelegationDetail
	receipts := map[string]agent.ConversationResultReference{}
	contents := map[string]json.RawMessage{}
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Delegate professional request:\n") {
			target = strings.TrimPrefix(message.Content, "Delegate professional request:\n")
		}
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
			id = match[1]
		}
		if message.Role != "tool" {
			continue
		}
		var wire struct {
			agent.ConversationToolResult
			Reference *agent.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" {
			return agent.ConversationStepResult{}, fmt.Errorf("professional source tool did not complete: %s", message.ToolCallID)
		}
		dispatched = dispatched || message.ToolCallID == "professional-dispatch"
		delivered = delivered || message.ToolCallID == "professional-deliver"
		contents[message.ToolCallID] = wire.Content
		if wire.Reference != nil {
			receipts[message.ToolCallID] = *wire.Reference
		}
		if message.ToolCallID == "professional-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
	}
	if target != "" && id == "" {
		if dispatched {
			return stop()
		}
		brief := agent.ConversationTaskBrief{Version: 1, Goal: "Compare the current all-customer report and aggregate analysis", Deliverable: "Both exact totals and their original report and analysis receipts", Constraints: []string{}, Assumptions: []string{}, CompletionConditions: []string{"Discover the current report catalog", "Query the all-customer balance report", "Discover the current customer dataset", "Analyze the current customer balance sum"}, VerificationRules: []agent.ConversationCompletionRule{
			{Condition: 0, Kind: "receipt", Tool: "report_query", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"const":"catalog"}},"required":["operation"]}`), ResultSchema: json.RawMessage(`{"type":"object","required":["catalog"]}`)},
			{Condition: 1, Kind: "receipt", Tool: "report_query", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"const":"query"}},"required":["operation"]}`), ResultSchema: json.RawMessage(`{"type":"object","required":["result"]}`)},
			{Condition: 2, Kind: "receipt", Tool: "analysis_run", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"const":"catalog"}},"required":["operation"]}`), ResultSchema: json.RawMessage(`{"type":"object","required":["catalog"]}`)},
			{Condition: 3, Kind: "receipt", Tool: "analysis_run", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"const":"run"}},"required":["operation"]}`), ResultSchema: json.RawMessage(`{"type":"object","required":["result"]}`)},
		}}
		return tool("professional-dispatch", "agent_delegate", map[string]any{"agent_id": target, "purpose": "Independent professional query and analysis", "brief": brief, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 90}, "input": "Discover the current report and dataset, compare every returned balance with the exact aggregate total and submit original receipts", "requirements": agent.ConversationAgentRequirements{Tools: []string{"report_query", "analysis_run"}}})
	}
	if id == "" || delivered {
		return stop()
	}
	calls := []struct{ id, name, args string }{
		{"professional-report-catalog", "report_query", `{"operation":"catalog"}`},
		{"professional-report", "report_query", `{"operation":"query","report_key":"customer_balances"}`},
		{"professional-analysis-catalog", "analysis_run", `{"operation":"catalog","dataset_key":"customer"}`},
		{"professional-analysis", "analysis_run", `{"operation":"run","spec":{"dataset_key":"customer","measures":[{"key":"total","function":"sum","field":"balance"}]}}`},
	}
	for _, call := range calls {
		if _, ok := receipts[call.id]; !ok {
			return tool(call.id, call.name, json.RawMessage(call.args))
		}
	}
	if detail.ID == "" {
		return tool("professional-agreement", "delegation_get", map[string]string{"id": id})
	}
	var reportResult struct {
		Result *model.ReportQueryResult `json:"result"`
	}
	var analysisResult struct {
		Result *model.AnalysisResult `json:"result"`
	}
	if json.Unmarshal(contents["professional-report"], &reportResult) != nil || reportResult.Result == nil || reportResult.Result.Summary.Truncated || json.Unmarshal(contents["professional-analysis"], &analysisResult) != nil || analysisResult.Result == nil || len(analysisResult.Result.Rows) != 1 || analysisResult.Result.Rows[0].Values["total"] == nil {
		return agent.ConversationStepResult{}, fmt.Errorf("incomplete original professional values")
	}
	var total int64
	for _, row := range reportResult.Result.Summary.Rows {
		value, err := strconv.ParseInt(row.Dimensions["balance"], 10, 64)
		if err != nil {
			return agent.ConversationStepResult{}, err
		}
		total += value
	}
	reportTotal, analysisTotal := strconv.FormatInt(total, 10), *analysisResult.Result.Rows[0].Values["total"]
	if reportTotal != analysisTotal {
		return agent.ConversationStepResult{}, fmt.Errorf("original query and aggregate disagree")
	}
	conditions := []agent.ConversationConditionAssessment{
		{Condition: 0, Verdict: "met", Basis: "The original current report catalog was discovered", Receipts: []agent.ConversationResultReference{receipts["professional-report-catalog"]}},
		{Condition: 1, Verdict: "met", Basis: "Every returned report row summed exactly to " + reportTotal, Receipts: []agent.ConversationResultReference{receipts["professional-report"]}},
		{Condition: 2, Verdict: "met", Basis: "The original current customer dataset was discovered", Receipts: []agent.ConversationResultReference{receipts["professional-analysis-catalog"]}},
		{Condition: 3, Verdict: "met", Basis: "The complete original aggregate equals " + analysisTotal, Receipts: []agent.ConversationResultReference{receipts["professional-analysis"]}},
	}
	return tool("professional-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Compared exact values from original professional results", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Report and analysis both equal " + analysisTotal, Data: json.RawMessage(sharedProfessionalJSON(map[string]string{"report_total": reportTotal, "analysis_total": analysisTotal})), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}

func sharedProfessionalCollaborationRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		seen := map[string]bool{}
		for _, value := range role["permissions"].([]any) {
			seen[value.(map[string]any)["permission_key"].(string)] = true
		}
		keys := []string{agent.ConversationInteractionPermission().Key}
		for _, op := range agent.ConversationCollaborationOperations() {
			keys = append(keys, agent.ConversationCollaborationPermission(op).Key)
		}
		for _, definition := range agent.ConversationCollaborationTools() {
			keys = append(keys, definition.ActionKey)
		}
		for _, key := range keys {
			if !seen[key] {
				role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
				seen[key] = true
			}
		}
	}
}

func TestCrossUserAgentProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead(t *testing.T) {
	verifyProfessionalDispatchAndCurrentSourceRead(t, false)
}

func TestSameUserDifferentRolesProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead(t *testing.T) {
	verifyProfessionalDispatchAndCurrentSourceRead(t, true)
}

func sameUserProfessionalReaderRoles(m map[string]any) {
	// The browser uses Identity's current deterministic default role. Keep
	// the separately assigned headquarters role available to the bound Agent
	// while selecting a results-only browser role through real assignments.
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		key := role["key"].(string)
		if !strings.HasPrefix(key, "results_") {
			continue
		}
		raw, _ := json.Marshal(role)
		var clone map[string]any
		_ = json.Unmarshal(raw, &clone)
		clone["key"], clone["name"] = "a_"+key, "a_"+key
		m["roles"] = append(m["roles"].([]any), clone)
	}
}

func verifyProfessionalDispatchAndCurrentSourceRead(t *testing.T, sameUser bool) {
	t.Helper()
	f := newBusinessWebFixtureWithModel(t, sharedProfessionalDeliveryModel{}, businessRPCManifest, businessReportManifest, analysisToolsManifest, sharedResultUserRoles, sharedProfessionalCollaborationRoles, resultReadRoles, sameUserProfessionalReaderRoles)
	issuer := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	issuer.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	issuer.assign("headquarters_admin")
	issuer.session()
	receiver, receiverID := issuer, "admin"
	users, mode := []string{"admin"}, "owner"
	if sameUser {
		users = []string{}
	} else {
		receiver, receiverID = createSharedResultProducer(t, f, issuer), "professional_source"
	}
	var professional agent.ConversationAgent
	if err := json.Unmarshal(receiver.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "shared-professional-agent", Name: "Professional report analyst", Instructions: "Use original report and analysis results, compare their totals, then submit their immutable receipts", Tools: []string{"report_query", "analysis_run", "delegation_get", "delegation_update"}, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}, 200).Body.Bytes(), &professional); err != nil || professional.DelegationRoleKey != "headquarters_admin" || professional.OwnerUserID != receiverID {
		t.Fatal("professional identity binding", professional, err)
	}
	assignReader := func(role string) {
		t.Helper()
		if !sameUser {
			issuer.assign(role)
			return
		}
		assignments := []any{map[string]any{"role_id": "a_" + role}, map[string]any{"role_id": "headquarters_admin"}}
		if role != "results_reader" {
			// Keep the original issuer role valid so field/row denial measures
			// the current reader policy rather than contract-role revocation.
			assignments = append(assignments, map[string]any{"role_id": "a_results_reader"})
		}
		managedIdentityRequest(t, sharedResultIdentityHandler(f), issuer.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/admin/account-and-roles", map[string]any{"user": map[string]any{"name": "Admin", "email": "admin@example.com", "status": "active"}, "assignments": assignments}, fmt.Sprintf("same-user-reader-role-%d", time.Now().UnixNano()), 200)
		issuer.call("POST", "/auth/refresh", map[string]any{}, 200)
	}
	assignReader("results_reader")
	issuer.session()
	me, err := f.identity.Authentication().CurrentSession(t.Context(), identity.CurrentSessionRequest{AccessToken: issuer.cookies["domainry_agent_access"].Value})
	if err != nil {
		t.Fatal(err)
	}
	if sameUser && me.DefaultRole != "a_results_reader" {
		t.Fatal("browser did not select the separate results-only role", me.DefaultRole)
	}
	var source agent.Conversation
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "professional-dispatch-source", Title: "Delegate to report professional"}, 200).Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	var sent agent.ConversationRun
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", agent.ConversationSend{ClientMessageID: "professional-tool-dispatch", Message: "Delegate professional request:\n" + professional.ID}, 202).Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	var detail agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var current agent.ConversationRun
		if err := json.Unmarshal(issuer.call("GET", "/agent/conversations/"+source.ID+"/runs/"+sent.ID, nil, 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if i := current.Interaction; i != nil && i.Status == "pending" {
			issuer.call("POST", "/agent/conversations/"+source.ID+"/runs/"+sent.ID+"/respond", agent.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}, 200)
		}
		var page agent.ConversationDelegationPage
		if err := json.Unmarshal(issuer.call("GET", "/agent/delegations", nil, 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 0 {
			if err := json.Unmarshal(receiver.call("GET", "/agent/delegations/"+page.Items[0].ID, nil, 200).Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Task != nil && detail.Task.ExecutionRunID != "" {
				if err := json.Unmarshal(receiver.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+detail.Task.ExecutionRunID, nil, 200).Body.Bytes(), &execution); err != nil {
					t.Fatal(err)
				}
				if execution.Interaction != nil && execution.Interaction.Status == "pending" {
					t.Fatal("receiver professional execution unexpectedly required user confirmation", execution.Interaction)
				}
				if execution.Terminal() {
					break
				}
			}
		} else if current.Terminal() {
			t.Fatal("Agent tool failed to create delegation", current)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || detail.Delivery == nil || detail.ExecutionSubject == nil || detail.ExecutionSubject.UserID != receiverID || execution.Agent == nil || execution.Agent.DelegationRoleKey != professional.DelegationRoleKey {
		t.Fatal("professional automatic delivery incomplete", detail, execution)
	}
	for _, condition := range detail.Delivery.Conditions {
		for _, ref := range condition.Receipts {
			issuer.call("POST", "/agent/delegations/"+detail.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, 200)
		}
	}
	var published agent.ConversationDelegationDetail
	if err := json.Unmarshal(issuer.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &published); err != nil || published.Delivery == nil || published.Verification == nil || !published.Verification.Ready || !strings.Contains(published.Delivery.Summary, "70") {
		t.Fatalf("reader professional delivery: delivery=%s verification=%s access=%s error=%v", sharedProfessionalJSON(published.Delivery), sharedProfessionalJSON(published.Verification), sharedProfessionalJSON(published.Access), err)
	}
	refs := []agent.ConversationResultReference{}
	for _, condition := range published.Delivery.Conditions {
		refs = append(refs, condition.Receipts...)
	}
	if len(refs) != 4 {
		t.Fatal("original professional receipts missing", refs)
	}
	readPath := "/agent/delegations/" + detail.ID + "/delivery-result"
	read := func(want int) {
		t.Helper()
		for _, ref := range refs {
			issuer.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
		}
	}
	read(200)
	if sameUser {
		var rawExecution agent.ConversationRun
		if err := json.Unmarshal(issuer.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+execution.ID, nil, 200).Body.Bytes(), &rawExecution); err != nil {
			t.Fatal(err)
		}
		if rawExecution.AccessError == "" || len(rawExecution.Steps) != 0 {
			t.Fatal("delivery read granted raw professional execution replay", rawExecution.AccessError, sharedProfessionalJSON(rawExecution.Steps))
		}
	} else {
		issuer.call("GET", "/agent/conversations/"+detail.ConversationID, nil, 404)
		receiver.call("GET", "/agent/conversations/"+source.ID, nil, 404)
	}
	unpublished := refs[0]
	unpublished.CallID = "unpublished-professional-call"
	issuer.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: unpublished}}, 403)
	issuer.call("POST", "/agent/delegations/"+detail.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-professional-delivery", ExpectedRevision: published.Revision, Action: "accept_delivery", Reason: "Checked both original professional results and their exact totals", Review: &agent.ConversationDeliveryReview{DeliveryDigest: published.Verification.DeliveryDigest}}, 200)
	f.close()
	f.open()
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	issuer.session()
	read(200)
	assignReader("results_field_denied")
	issuer.session()
	read(403)
	assignReader("results_reader")
	issuer.session()
	read(200)
	assignReader("results_narrow")
	issuer.session()
	narrowReads := 0
	for _, condition := range published.Delivery.Conditions {
		for _, ref := range condition.Receipts {
			if ref.CallID == "professional-report" || ref.CallID == "professional-analysis" {
				narrowReads++
				issuer.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, 403)
			}
		}
	}
	if narrowReads != 2 {
		t.Fatal("narrow source projection was not tested for both results", narrowReads)
	}
	assignReader("results_reader")
	issuer.session()
	if sameUser {
		issuer.assign("a_results_reader")
		issuer.session()
	} else {
		assignSharedResultProducer(t, f, issuer, "results_data_denied")
	}
	read(403)
}
