package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
)

type sharedBusinessDeliveryModel struct {
	businessWebModel
	complexQuery bool
}

func (m sharedBusinessDeliveryModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	if m.complexQuery {
		return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "shared-complex-business-delivery", Fingerprint: "shared-complex-business-delivery-v1"}
	}
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "shared-business-delivery", Fingerprint: "shared-business-delivery-v1"}
}

func (m sharedBusinessDeliveryModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, name string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: name, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "已提交实际业务目录、查询、详情和关联记录的原回执。"}}, nil
	}
	target, id, dispatched, delivered := "", "", false, false
	var detail agent.ConversationDelegationDetail
	refs := map[string]agent.ConversationResultReference{}
	data := map[string]json.RawMessage{}
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Delegate business request:\n") {
			target = strings.TrimPrefix(message.Content, "Delegate business request:\n")
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
			return agent.ConversationStepResult{}, fmt.Errorf("business tool failed: %s", message.ToolCallID)
		}
		dispatched = dispatched || message.ToolCallID == "business-dispatch"
		delivered = delivered || message.ToolCallID == "business-deliver"
		if wire.Reference != nil {
			refs[message.ToolCallID] = *wire.Reference
		}
		var evidence agent.ConversationBusinessEvidence
		if json.Unmarshal(wire.Content, &evidence) == nil && evidence.Version == 1 {
			data[message.ToolCallID] = evidence.Data
		}
		if message.ToolCallID == "business-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
	}
	keys := []string{"business_catalog", "query_records", "get_record", "query_related_records"}
	callIDs := []string{"business-catalog", "business-query", "business-get", "business-related"}
	getIndex, relatedIndex := 2, 3
	completionConditions := []string{"Discover customer schema", "Read the first customer page", "Read the discovered customer", "Read its discovered related orders"}
	if m.complexQuery {
		keys = []string{"business_catalog", "query_records", "query_records", "get_record", "query_related_records"}
		callIDs = []string{"business-catalog", "business-query", "business-query-next", "business-get", "business-related"}
		getIndex, relatedIndex = 3, 4
		completionConditions = []string{"Discover customer schema", "Read the first filtered customer page with original total", "Read the exact original cursor's second page", "Read the discovered customer", "Read its discovered related orders"}
	}
	if target != "" && id == "" {
		if dispatched {
			return stop()
		}
		brief := agent.ConversationTaskBrief{Version: 1, Goal: "Read the current customer catalog, authorized query pages, record and related orders", Deliverable: fmt.Sprintf("%d exact original business receipts", len(keys)), Constraints: []string{}, Assumptions: []string{}, CompletionConditions: completionConditions}
		for i, key := range keys {
			brief.VerificationRules = append(brief.VerificationRules, agent.ConversationCompletionRule{Condition: i, Kind: "receipt", Tool: key})
		}
		input := "Read current customer catalog, first page, discovered record and related orders; submit all exact original receipts"
		if m.complexQuery {
			input = "Read current customer catalog; filter by approved names, exclusions and balance range; read first and second original cursor pages, discovered record and related orders; submit all five exact receipts"
		}
		required := []string{"business_catalog", "query_records", "get_record", "query_related_records"}
		return tool("business-dispatch", "agent_delegate", map[string]any{"agent_id": target, "purpose": "Delegate authorized business reads to the configuration owner's independent identity", "brief": brief, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 90}, "input": input, "requirements": agent.ConversationAgentRequirements{Tools: required}})
	}
	if id == "" || delivered {
		return stop()
	}
	if _, ok := refs[callIDs[0]]; !ok {
		return tool(callIDs[0], keys[0], agent.ConversationBusinessCatalogQuery{ObjectKey: "customer"})
	}
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}
	if m.complexQuery {
		q.Filters = []agent.ConversationBusinessFilter{
			{Field: "name", Operator: "in", Value: json.RawMessage(`["Alpha","Beta","Gamma","Other"]`)},
			{Field: "name", Operator: "not_in", Value: json.RawMessage(`["Other"]`)},
			{Field: "balance", Operator: "gte", Value: json.RawMessage(`10`)},
			{Field: "balance", Operator: "lt", Value: json.RawMessage(`35`)},
		}
	}
	if _, ok := refs[callIDs[1]]; !ok {
		return tool(callIDs[1], keys[1], q)
	}
	var page agent.ConversationBusinessRecordPage
	if json.Unmarshal(data[callIDs[1]], &page) != nil || len(page.Items) != 1 {
		return agent.ConversationStepResult{}, fmt.Errorf("query did not yield an actual original record")
	}
	if m.complexQuery {
		if page.Total == nil || *page.Total != 3 || page.NextCursor == "" || string(page.Items[0].Data["name"]) != `"Alpha"` || string(page.Items[0].Data["balance"]) != "10" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual filtered first page or total changed")
		}
		if _, ok := refs["business-query-next"]; !ok {
			q.Cursor = page.NextCursor
			return tool("business-query-next", "query_records", q)
		}
		var next agent.ConversationBusinessRecordPage
		if json.Unmarshal(data["business-query-next"], &next) != nil || next.Page != 2 || next.Total != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || string(next.Items[0].Data["name"]) != `"Beta"` || string(next.Items[0].Data["balance"]) != "20" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual filtered original second page changed")
		}
	}
	if _, ok := refs[callIDs[getIndex]]; !ok {
		return tool(callIDs[getIndex], keys[getIndex], agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: page.Items[0].ID, Fields: q.Fields})
	}
	if _, ok := refs[callIDs[relatedIndex]]; !ok {
		var catalog agent.ConversationBusinessCatalogPage
		if json.Unmarshal(data[callIDs[0]], &catalog) != nil || len(catalog.Items) != 1 {
			return agent.ConversationStepResult{}, fmt.Errorf("original customer schema missing")
		}
		for _, relation := range catalog.Items[0].Relations {
			if relation.ObjectKey == "order" {
				return tool(callIDs[relatedIndex], keys[relatedIndex], agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: page.Items[0].ID, RelationKey: relation.Key, Fields: []string{"name", "amount"}, PageSize: 1})
			}
		}
		return agent.ConversationStepResult{}, fmt.Errorf("actual order relation missing from original catalog")
	}
	if detail.ID == "" {
		return tool("business-agreement", "delegation_get", map[string]string{"id": id})
	}
	conditions := []agent.ConversationConditionAssessment{}
	for i, callID := range callIDs {
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Read the source-owned original " + keys[i] + " result", Receipts: []agent.ConversationResultReference{refs[callID]}})
	}
	return tool("business-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Completed all declared business reads with original receipts", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Original customer and related order receipts", Data: json.RawMessage(sharedProfessionalJSON(map[string]int{"business_receipts": len(keys)})), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}

func sharedBusinessIssuerTools(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if !strings.HasPrefix(role["key"].(string), "shared_business_") {
			continue
		}
		for _, key := range []string{"agent_delegate", "delegation_get", "delegation_update"} {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": agent.ConversationToolActionPrefix + key, "data_scope": "all"})
		}
	}
}

func TestCrossUserBusinessAgentToolDispatchAutomaticDeliveryOriginalPagesAndAcceptance(t *testing.T) {
	exerciseSharedBusinessAgentDelivery(t, false)
}

func TestCrossUserBusinessAgentComplexFiltersOriginalCursorAutomaticDeliveryAndAcceptance(t *testing.T) {
	exerciseSharedBusinessAgentDelivery(t, true)
}

func sharedBusinessComplexQueryRecords(m map[string]any) {
	for _, value := range m["seed_records"].([]any) {
		seed := value.(map[string]any)
		if seed["object_key"] != "customer" || seed["owner_user_id"] != "admin" {
			continue
		}
		data := seed["data"].(map[string]any)
		if data["name"] != "Beta" && data["name"] != "Gamma" {
			data["name"] = "Alpha"
		}
	}
}

// Use the exact admitted tool receipts, rather than sealing newly queried data.
// A reader may inspect a producer's saved cursor page but must start its own
// query when accessing current records through the public owner RPC.
func verifyDeliveredBusinessCursorRPC(t *testing.T, issuer *businessBrowser, detail agent.ConversationDelegationDetail, refs []agent.ConversationResultReference, producerRole string) {
	t.Helper()
	evidence := map[string]agent.ConversationBusinessEvidence{}
	for _, ref := range refs {
		if ref.CallID != "business-query" && ref.CallID != "business-query-next" {
			continue
		}
		var slice agent.ConversationResultSlice
		response := issuer.call("POST", "/agent/delegations/"+detail.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, 200)
		if err := json.Unmarshal(response.Body.Bytes(), &slice); err != nil || !slice.Complete || slice.Reference != ref {
			t.Fatal("exact original query receipt unavailable", ref.CallID, err)
		}
		var result agent.ConversationToolResult
		var original agent.ConversationBusinessEvidence
		if json.Unmarshal([]byte(slice.JSONText), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &original) != nil || original.Operation != "query_records" || original.HostProof == "" {
			t.Fatal("original query proof absent", ref.CallID)
		}
		evidence[ref.CallID] = original
	}
	var first agent.ConversationBusinessRecordPage
	var q agent.ConversationBusinessQuery
	if len(evidence) != 2 || json.Unmarshal(evidence["business-query"].Data, &first) != nil || first.NextCursor == "" || json.Unmarshal(evidence["business-query-next"].Input, &q) != nil || q.Cursor != first.NextCursor {
		t.Fatal("delivered second page did not retain the exact original cursor")
	}
	reader := agent.ConversationAuthority{Known: true, RuntimeID: issuer.f.cfg.RuntimeInstanceID, WorkspaceID: issuer.f.cfg.IdentityWorkspaceID, UserID: "admin", RoleKey: "shared_business_reader"}
	producer := reader
	producer.UserID, producer.RoleKey = detail.ExecutionSubject.UserID, producerRole
	client, server := openAnalysisRPC(t, issuer.f, reader)
	defer server.Close()
	for _, original := range evidence {
		if err := client.AuthorizeSharedBusinessResultRead(t.Context(), original, reader, producer); err != nil {
			t.Fatal("actual reader cannot authorize an exact delivered query page through RPC", err)
		}
	}
	// The public owner RPC checks execution authority before cursor validation.
	// First prove the read-only role cannot invoke it, then give this different
	// user an actual execution role to isolate the cursor's producer binding.
	if _, err := client.QueryBusinessRecords(t.Context(), q, reader); err == nil {
		t.Fatal("shared reading granted query execution to a read-only role")
	} else {
		var failure *agent.Error
		if !errors.As(err, &failure) || failure.Class != "forbidden" {
			t.Fatal("read-only role failed outside the execution authorization gate", err)
		}
	}
	issuer.assign("headquarters_admin")
	issuer.session()
	reader.RoleKey = "headquarters_admin"
	originalCursor := q.Cursor
	q.Cursor = ""
	current, err := client.QueryBusinessRecords(t.Context(), q, reader)
	if err != nil || current.Total == nil || *current.Total != 3 || len(current.Items) != 1 {
		t.Fatal("reader's own fresh filtered query was not authorized", current, err)
	}
	q.Cursor = originalCursor
	if _, err := client.QueryBusinessRecords(t.Context(), q, reader); err == nil {
		t.Fatal("execution-authorized reader acquired the original producer cursor")
	} else {
		var failure *agent.Error
		if !errors.As(err, &failure) || failure.Class != "bad_request" {
			t.Fatal("foreign cursor failed for a reason other than cursor validation", err)
		}
	}
	issuer.assign("shared_business_reader")
	issuer.session()
	t.Log("Original delivered pages authorized through actual owner RPC without execution rights; execution-authorized reader's own query accepted and foreign producer cursor rejected")
}

func exerciseSharedBusinessAgentDelivery(t *testing.T, complexQuery bool) {
	t.Helper()
	customize := []func(map[string]any){businessRPCManifest, independentBusinessReadRoles, sharedResultUserRoles, sharedProfessionalCollaborationRoles, sharedBusinessReceiptRoles, sharedBusinessIssuerTools}
	if complexQuery {
		customize = append(customize, sharedBusinessComplexQueryRecords)
	}
	f := newBusinessWebFixtureWithModel(t, sharedBusinessDeliveryModel{complexQuery: complexQuery}, customize...)
	issuer := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	issuer.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	issuer.assign("headquarters_admin")
	issuer.session()
	receiver := createSharedResultProducer(t, f, issuer)
	users, mode := []string{"admin"}, "owner"
	var professional agent.ConversationAgent
	if err := json.Unmarshal(receiver.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "shared-business-agent", Name: "Independent business reader", Instructions: "Read original customer and related order receipts, then deliver each exact receipt", Tools: []string{"business_catalog", "query_records", "get_record", "query_related_records", "delegation_get", "delegation_update"}, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}, 200).Body.Bytes(), &professional); err != nil || professional.DelegationRoleKey != "headquarters_admin" {
		t.Fatal("business execution binding", professional, err)
	}
	issuer.assign("shared_business_reader")
	issuer.session()
	var source agent.Conversation
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "shared-business-dispatch", Title: "Delegate business reads"}, 200).Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	var sent agent.ConversationRun
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", agent.ConversationSend{ClientMessageID: "business-tool-dispatch", Message: "Delegate business request:\n" + professional.ID}, 202).Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	var detail agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	// Setup, dispatch and reading the source run consume part of this outer
	// observation window, especially under the race detector. The delegated
	// task still owns its exact 90-second budget; observe the service's real
	// terminal state instead of tearing the fixture down while it is running.
	deadline := time.Now().Add(5 * time.Minute)
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
				if execution.Terminal() {
					break
				}
			}
		} else if current.Terminal() {
			t.Fatal("business Agent tool did not dispatch", current)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || detail.Delivery == nil || detail.ExecutionSubject == nil || detail.ExecutionSubject.UserID != "professional_source" {
		t.Fatalf("business automatic delivery incomplete: task=%s run=%s delivery=%s", sharedProfessionalJSON(detail.Task), sharedProfessionalJSON(execution), sharedProfessionalJSON(detail.Delivery))
	}
	var published agent.ConversationDelegationDetail
	path := "/agent/delegations/" + detail.ID
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil || published.Verification == nil || !published.Verification.Ready || published.Delivery == nil {
		t.Fatal("business delivery verification", published, err)
	}
	refs := []agent.ConversationResultReference{}
	for _, condition := range published.Delivery.Conditions {
		refs = append(refs, condition.Receipts...)
	}
	expectedReceipts := 4
	if complexQuery {
		expectedReceipts = 5
	}
	if len(refs) != expectedReceipts {
		t.Fatal("original business receipts missing", refs)
	}
	read := func(want int) {
		t.Helper()
		for _, ref := range refs {
			issuer.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
		}
	}
	read(200)
	sharedExecutionRead := verifyProfessionalExecutionSharingSteps(t, issuer, receiver, detail, execution, refs, expectedReceipts+3)
	if complexQuery {
		verifyDeliveredBusinessCursorRPC(t, issuer, detail, refs, professional.DelegationRoleKey)
	}
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	issuer.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-business-originals", ExpectedRevision: published.Revision, Action: "accept_delivery", Reason: fmt.Sprintf("Checked %d original business results and shared execution pages", expectedReceipts), Review: &agent.ConversationDeliveryReview{DeliveryDigest: published.Verification.DeliveryDigest}}, 200)
	var restoreProducerExecution func()
	if complexQuery {
		restoreProducerExecution = withdrawSharedBusinessProducerExecution(t, f)
		read(200)
		sharedExecutionRead(200)
	}
	f.close()
	f.open()
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	issuer.session()
	read(200)
	sharedExecutionRead(200)
	issuer.assign("shared_business_field_denied")
	issuer.session()
	read(403)
	sharedExecutionRead(403)
	issuer.assign("shared_business_reader")
	issuer.session()
	read(200)
	assignSharedResultProducer(t, f, issuer, "shared_business_data_denied")
	read(403)
	sharedExecutionRead(403)
	issuer.call("GET", "/agent/conversations/"+detail.ConversationID, nil, 404)
	if restoreProducerExecution != nil {
		assignSharedResultProducer(t, f, issuer, professional.DelegationRoleKey)
		receiver.session()
		read(200)
		sharedExecutionRead(200)
		restoreProducerExecution()
	}
}
