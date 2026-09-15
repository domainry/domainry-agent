package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type sharedKnowledgeDeliveryModel struct {
	businessWebModel
	legacy bool
}

func (m sharedKnowledgeDeliveryModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	if m.legacy {
		return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "shared-legacy-knowledge-delivery", Fingerprint: "shared-legacy-knowledge-delivery-v1"}
	}
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "shared-knowledge-delivery", Fingerprint: "shared-knowledge-delivery-v1"}
}

func (m sharedKnowledgeDeliveryModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, key string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: key, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "已提交资料检索、正文及金额抽取的原回执。"}}, nil
	}
	target, id, dispatched, delivered := "", "", false, false
	var detail agent.ConversationDelegationDetail
	refs := map[string]agent.ConversationResultReference{}
	evidence := map[string]agent.ConversationKnowledgeResult{}
	var extracted agent.KnowledgeExtractionResult
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Delegate knowledge request:\n") {
			target = strings.TrimPrefix(message.Content, "Delegate knowledge request:\n")
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
			return agent.ConversationStepResult{}, fmt.Errorf("knowledge tool failed: %s", message.ToolCallID)
		}
		dispatched = dispatched || message.ToolCallID == "knowledge-dispatch"
		delivered = delivered || message.ToolCallID == "knowledge-deliver"
		if wire.Reference != nil {
			refs[message.ToolCallID] = *wire.Reference
		}
		var saved agent.ConversationKnowledgeResult
		if json.Unmarshal(wire.Content, &saved) == nil && saved.Provider != "" {
			evidence[message.ToolCallID] = saved
		}
		if message.ToolCallID == "knowledge-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
		if message.ToolCallID == "knowledge-extract" {
			if err := json.Unmarshal(wire.Content, &extracted); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
	}
	keys := []string{"knowledge_libraries", "knowledge_search", "knowledge_read", "knowledge_extract"}
	ids := []string{"knowledge-catalog", "knowledge-search", "knowledge-read", "knowledge-extract"}
	completionConditions := []string{"Discover shared library", "Search amount source", "Read discovered original document", "Extract original amount"}
	if m.legacy {
		keys, ids, completionConditions = keys[1:], ids[1:], completionConditions[1:]
	}
	if target != "" && id == "" {
		if dispatched {
			return stop()
		}
		brief := agent.ConversationTaskBrief{Version: 1, Goal: "Read the shared original amount source and extract its declared amount", Deliverable: fmt.Sprintf("%d original knowledge receipts", len(keys)), Constraints: []string{}, Assumptions: []string{}, CompletionConditions: completionConditions}
		for i, key := range keys {
			brief.VerificationRules = append(brief.VerificationRules, agent.ConversationCompletionRule{Condition: i, Kind: "receipt", Tool: key})
		}
		input := "Search amount; read its actual cited document; extract amount with 金额：([0-9.]+); deliver exact original receipts"
		if !m.legacy {
			input = "Discover shared knowledge library; " + input
		}
		return tool("knowledge-dispatch", "agent_delegate", map[string]any{"agent_id": target, "purpose": "Read and extract from the execution user's currently authorized original source", "brief": brief, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 90}, "input": input, "requirements": agent.ConversationAgentRequirements{Tools: keys}})
	}
	if id == "" || delivered {
		return stop()
	}
	library := ""
	if !m.legacy {
		if _, ok := refs["knowledge-catalog"]; !ok {
			return tool("knowledge-catalog", "knowledge_libraries", map[string]any{})
		}
		var page agent.KnowledgeLibraryPage
		if json.Unmarshal(evidence["knowledge-catalog"].Data, &page) != nil {
			return agent.ConversationStepResult{}, fmt.Errorf("actual library catalog missing")
		}
		for _, item := range page.Items {
			if item.Kind == "shared" {
				library = item.ID
				break
			}
		}
		if library == "" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual shared library missing")
		}
	}
	if _, ok := refs["knowledge-search"]; !ok {
		args := map[string]string{"query": "金额"}
		if library != "" {
			args["library_id"] = library
		}
		return tool("knowledge-search", "knowledge_search", args)
	}
	search := evidence["knowledge-search"]
	if len(search.Citations) == 0 || search.LibraryID != library {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original document citation missing")
	}
	document := search.Citations[0].DocumentID
	if _, ok := refs["knowledge-read"]; !ok {
		args := map[string]string{"doc_id": document}
		if library != "" {
			args["library_id"] = library
		}
		return tool("knowledge-read", "knowledge_read", args)
	}
	if _, ok := refs["knowledge-extract"]; !ok {
		return tool("knowledge-extract", "knowledge_extract", agent.KnowledgeExtractionArguments{LibraryID: library, DocumentID: document, KnowledgeExtractionPlan: agent.KnowledgeExtractionPlan{Fields: []agent.KnowledgeExtractionField{{KnowledgeExtractionColumn: agent.KnowledgeExtractionColumn{Key: "amount", Type: "decimal", Required: true}, Pattern: `金额：([0-9.]+)`}}}})
	}
	if extracted.Operation != "extract" || extracted.DocumentID != document || extracted.LibraryID != library || extracted.PlanSHA256 == "" || sharedProfessionalJSON(extracted.Source) != sharedProfessionalJSON(evidence["knowledge-read"]) || len(extracted.Data.Fields) != 1 {
		return agent.ConversationStepResult{}, fmt.Errorf("extraction did not preserve its actual original document")
	}
	cell := extracted.Data.Fields[0]
	if cell.Key != "amount" || cell.Type != "decimal" || cell.Status != "valid" || cell.Value == nil || *cell.Value != "123.45" || len(cell.Evidence) != 1 || cell.Evidence[0].Quote != "123.45" || cell.Evidence[0].CitationID == "" {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original amount and cited extraction span missing: %s", sharedProfessionalJSON(cell))
	}
	if detail.ID == "" {
		return tool("knowledge-agreement", "delegation_get", map[string]string{"id": id})
	}
	conditions := []agent.ConversationConditionAssessment{}
	for i, call := range ids {
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Actual original " + keys[i] + " receipt", Receipts: []agent.ConversationResultReference{refs[call]}})
	}
	return tool("knowledge-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Read and extracted the original shared source", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Original shared knowledge source amount 123.45", Data: json.RawMessage(sharedProfessionalJSON(map[string]any{"amount": *cell.Value, "original_receipts": len(keys)})), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}

func sharedKnowledgeSourcePermissions(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, definition := range agent.ConversationHTTPDefinitions() {
			permission := agent.KnowledgeLibraryPermission(definition.Operation)
			if permission == nil {
				permission = agent.KnowledgeDocumentPermission(definition.Operation)
			}
			if permission != nil {
				role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": permission.Key, "data_scope": "all"})
			}
		}
		for _, definition := range append(agent.LibraryKnowledgeConversationTools(), agent.KnowledgeExtractionTool()) {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": definition.ActionKey, "data_scope": "all"})
		}
	}
}

func sharedKnowledgeDeniedReadRole(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "shared_business_reader" {
			continue
		}
		raw, _ := json.Marshal(role)
		var clone map[string]any
		_ = json.Unmarshal(raw, &clone)
		clone["key"], clone["name"] = "shared_knowledge_download_denied", "Shared knowledge download denied"
		permissions := []any{}
		for _, value := range clone["permissions"].([]any) {
			p := value.(map[string]any)
			if p["permission_key"] != agent.KnowledgeDocumentPermission("documents_download").Key {
				permissions = append(permissions, p)
			}
		}
		clone["permissions"] = permissions
		m["roles"] = append(m["roles"].([]any), clone)
	}
}

func TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart(t *testing.T) {
	exerciseCrossUserKnowledgeAgentDispatch(t, "managed")
}

func TestCrossUserUnmanagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart(t *testing.T) {
	exerciseCrossUserKnowledgeAgentDispatch(t, "unmanaged")
}

func TestCrossUserLegacyKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart(t *testing.T) {
	exerciseCrossUserKnowledgeAgentDispatch(t, "legacy")
}

func exerciseCrossUserKnowledgeAgentDispatch(t *testing.T, mode string) {
	t.Helper()
	managed, legacy := mode == "managed", mode == "legacy"
	var mu sync.Mutex
	type original struct{ name, body string }
	files := map[string]original{}
	if !managed {
		files["original-upstream-document"] = original{"Amount-source.txt", "金额：123.45\n资料编号：9007199254740993\n"}
	}
	puts := 0
	changed := false
	readerRevoked := false
	producerRevoked := false
	aclRequests := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/documents") {
			id := r.URL.Query().Get("doc_id")
			if r.Method == "POST" {
				data, _ := io.ReadAll(r.Body)
				files[id] = original{r.URL.Query().Get("filename"), string(data)}
				puts++
			} else {
				delete(files, id)
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		if !managed {
			ids, ok := input["permission_ids"].([]any)
			if !ok || len(ids) != 1 || (ids[0] != "reader-document" && ids[0] != "producer-document") {
				t.Errorf("upstream received an invented user ACL: %v", input["permission_ids"])
				w.WriteHeader(http.StatusForbidden)
				return
			}
			aclRequests[ids[0].(string)]++
		}
		item := func(id string, f original) map[string]any {
			body := f.body
			if changed {
				body = "金额：999.99"
			}
			return map[string]any{"doc_id": id, "status": "INDEXED", "title": f.name, "body": body}
		}
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			for id, f := range files {
				hits = append(hits, item(id, f))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		id, _ := input["doc_id"].(string)
		f, ok := files[id]
		if !ok {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": item(id, f)})
	}))
	defer upstream.Close()
	f := prepareBusinessWebFixtureWithModel(t, sharedKnowledgeDeliveryModel{legacy: legacy}, businessRPCManifest, independentBusinessReadRoles, sharedResultUserRoles, sharedProfessionalCollaborationRoles, sharedKnowledgeSourcePermissions, sharedBusinessReceiptRoles, sharedBusinessIssuerTools, sharedKnowledgeDeniedReadRole)
	if managed {
		documentFiles, err := knowledgemodule.NewDocumentFiles(filepath.Join(t.TempDir(), "documents"))
		if err != nil {
			t.Fatal(err)
		}
		f.options.ConversationOptions.DocumentStorage = documentFiles
		t.Cleanup(func() {
			f.close()
			if err := documentFiles.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	mapping := &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}
	config := agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-key", TeamID: "team", KBID: "project", WorkspaceID: f.cfg.IdentityWorkspaceID, ResponseMapping: mapping}
	if managed {
		f.options.KnowledgeDatasources = []agentmodule.KnowledgeDatasourceConfig{{Key: "source", Name: "Shared original amount source", Knowledge: config}}
	} else {
		config.PermissionIDs = func(_ context.Context, a agent.ConversationAuthority) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			if a.Known && a.RuntimeID == f.cfg.RuntimeInstanceID && a.UserID == "professional_source" && a.RoleKey == "headquarters_admin" && a.WorkspaceID == f.cfg.IdentityWorkspaceID && !producerRevoked {
				return []string{"producer-document"}, nil
			}
			if a.Known && a.RuntimeID == f.cfg.RuntimeInstanceID && a.UserID == "admin" && a.RoleKey == "shared_business_reader" && a.WorkspaceID == f.cfg.IdentityWorkspaceID && !readerRevoked {
				return []string{"reader-document"}, nil
			}
			return nil, &agent.Error{Class: "forbidden", Code: "fixture.current_knowledge_reader_denied"}
		}
	}
	if legacy {
		f.options.Knowledge = config
	}
	f.open()
	issuer := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	issuer.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	assignReader := func(role string) {
		issuer.assign(role)
		issuer.call("POST", "/auth/refresh", map[string]any{}, 200)
		issuer.session()
	}
	assignReader("headquarters_admin")
	receiver := createSharedResultProducer(t, f, issuer)
	var library agent.KnowledgeLibrary
	libraryPath := ""
	if !legacy {
		if err := json.Unmarshal(issuer.call("POST", "/agent/knowledge-libraries", map[string]any{"client_id": "shared-original-amount-library", "kind": "shared", "name": "Original amount library"}, 200).Body.Bytes(), &library); err != nil {
			t.Fatal(err)
		}
		libraryPath = "/agent/knowledge-libraries/" + library.ID
		if managed {
			if err := json.Unmarshal(issuer.call("PUT", libraryPath+"/source", map[string]any{"datasource_key": "source", "expected_revision": library.Revision}, 200).Body.Bytes(), &library); err != nil {
				t.Fatal(err)
			}
		} else {
			// Bind the actual persisted library using the existing startup facade.
			// This upstream document has no local file/index record.
			f.close()
			f.options.KnowledgeLibraries = []agentmodule.KnowledgeLibraryConfig{{LibraryID: library.ID, Knowledge: config}}
			f.open()
		}
		if err := json.Unmarshal(issuer.call("PUT", libraryPath+"/members/professional_source", map[string]any{"role": "reader", "expected_revision": library.Revision}, 200).Body.Bytes(), &library); err != nil {
			t.Fatal(err)
		}
	}
	if managed {
		request := httptest.NewRequest("POST", businessWebOrigin+libraryPath+"/documents?"+url.Values{"client_id": {"original"}, "filename": {"Amount-source.txt"}}.Encode(), strings.NewReader("金额：123.45\n资料编号：9007199254740993\n"))
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("Origin", businessWebOrigin)
		request.Header.Set("X-Agent-Scope", issuer.scope)
		for _, c := range issuer.cookies {
			request.AddCookie(c)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatalf("original upload %d %s", w.Code, w.Body.String())
		}
		var doc agent.KnowledgeDocument
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(30 * time.Second); doc.State != "ready"; {
			if time.Now().After(deadline) {
				t.Fatal("original index not ready", doc)
			}
			time.Sleep(20 * time.Millisecond)
			if err := json.Unmarshal(issuer.call("GET", libraryPath+"/documents/"+doc.ID, nil, 200).Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
		}
	}
	tools := []string{"knowledge_libraries", "knowledge_search", "knowledge_read", "knowledge_extract", "delegation_get", "delegation_update"}
	if legacy {
		tools = tools[1:]
	}
	users, executionMode := []string{"admin"}, "owner"
	var professional agent.ConversationAgent
	if err := json.Unmarshal(receiver.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "shared-original-knowledge-agent", Name: "Independent knowledge source reader", Instructions: "Read shared original source and extract amount; submit exact original receipts", Tools: tools, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &executionMode}, 200).Body.Bytes(), &professional); err != nil || professional.DelegationRoleKey != "headquarters_admin" {
		t.Fatal("knowledge binding", professional, err)
	}
	assignReader("shared_business_reader")
	resolved, err := f.identity.Principals().Resolve(requestcontext.WithWorkspaceID(t.Context(), f.cfg.IdentityWorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: "admin", RoleKey: "shared_business_reader"})
	if err != nil || !resolved.Principal.Known || resolved.Principal.RoleKey != "shared_business_reader" {
		t.Fatalf("actual knowledge reader identity: %+v err=%v", resolved.Principal, err)
	}
	var source agent.Conversation
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "shared-original-knowledge-dispatch", Title: "Delegate shared original knowledge"}, 200).Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	var sent agent.ConversationRun
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", agent.ConversationSend{ClientMessageID: "knowledge-tool-dispatch", Message: "Delegate knowledge request:\n" + professional.ID}, 202).Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	var detail agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	executing := false
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
		if len(page.Items) == 0 {
			if current.Terminal() {
				t.Fatal("knowledge did not dispatch", current)
			}
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(receiver.call("GET", "/agent/delegations/"+page.Items[0].ID, nil, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Task != nil && detail.Task.ExecutionRunID != "" {
			if !executing {
				executing = true
				deadline = time.Now().Add(90 * time.Second)
			}
			if err := json.Unmarshal(receiver.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+detail.Task.ExecutionRunID, nil, 200).Body.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}
			if execution.Terminal() {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || detail.Delivery == nil || detail.ExecutionSubject == nil || detail.ExecutionSubject.UserID != "professional_source" {
		t.Fatalf("actual knowledge auto delivery incomplete: task=%s run=%s delivery=%s", sharedProfessionalJSON(detail.Task), sharedProfessionalJSON(execution), sharedProfessionalJSON(detail.Delivery))
	}
	path := "/agent/delegations/" + detail.ID
	var published agent.ConversationDelegationDetail
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil || published.Verification == nil || !published.Verification.Ready || published.Delivery == nil {
		t.Fatalf("knowledge delivery verification=%s error=%v", sharedProfessionalJSON(published.Verification), err)
	}
	refs := []agent.ConversationResultReference{}
	for _, condition := range published.Delivery.Conditions {
		refs = append(refs, condition.Receipts...)
	}
	expectedReceipts := 4
	if legacy {
		expectedReceipts = 3
	}
	if len(refs) != expectedReceipts {
		t.Fatal("original knowledge receipts missing", refs)
	}
	read := func(want int) {
		t.Helper()
		for _, ref := range refs {
			issuer.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
		}
	}
	read(200)
	sharedRead := verifyProfessionalExecutionSharingSteps(t, issuer, receiver, detail, execution, refs, expectedReceipts+3)
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	issuer.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-shared-original-knowledge", ExpectedRevision: published.Revision, Action: "accept_delivery", Reason: "Checked original library, citations, extraction and exact execution pages", Review: &agent.ConversationDeliveryReview{DeliveryDigest: published.Verification.DeliveryDigest}}, 200)
	restore := withdrawSharedBusinessProducerExecution(t, f)
	read(200)
	sharedRead(200)
	f.close()
	f.open()
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	issuer.session()
	read(200)
	sharedRead(200)
	if managed {
		assignReader("shared_knowledge_download_denied")
	} else {
		mu.Lock()
		readerRevoked = true
		mu.Unlock()
	}
	if managed {
		read(403)
	} else {
		// The catalog grants no document content. Its local library rights are
		// still valid while the upstream document ACL is withdrawn.
		for _, ref := range refs {
			want := 403
			if ref.CallID == "knowledge-catalog" {
				want = 200
			}
			issuer.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
		}
	}
	sharedRead(403)
	if managed {
		assignReader("shared_business_reader")
	} else {
		mu.Lock()
		readerRevoked = false
		mu.Unlock()
	}
	read(200)
	if !legacy {
		if err := json.Unmarshal(issuer.call("GET", libraryPath, nil, 200).Body.Bytes(), &library); err != nil {
			t.Fatal(err)
		}
		issuer.call("DELETE", libraryPath+"/members/professional_source?expected_revision="+fmt.Sprint(library.Revision), nil, 200)
		read(403)
		sharedRead(403)
		if err := json.Unmarshal(issuer.call("GET", libraryPath, nil, 200).Body.Bytes(), &library); err != nil {
			t.Fatal(err)
		}
		issuer.call("PUT", libraryPath+"/members/professional_source", map[string]any{"role": "reader", "expected_revision": library.Revision}, 200)
	} else {
		mu.Lock()
		producerRevoked = true
		mu.Unlock()
		read(403)
		sharedRead(403)
		mu.Lock()
		producerRevoked = false
		mu.Unlock()
	}
	read(200)
	mu.Lock()
	changed = true
	mu.Unlock()
	for _, ref := range refs {
		want := 409
		// A directory page contains no document body. The unchanged original
		// catalog stays readable; every content receipt and full run must deny.
		if ref.CallID == "knowledge-catalog" {
			want = 200
		}
		issuer.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
	}
	sharedRead(409)
	mu.Lock()
	changed = false
	mu.Unlock()
	read(200)
	sharedRead(200)
	restore()
	issuer.call("GET", "/agent/conversations/"+detail.ConversationID, nil, 404)
	mu.Lock()
	count := puts
	readerIO, producerIO := aclRequests["reader-document"], aclRequests["producer-document"]
	mu.Unlock()
	expectedUploads := 0
	if managed {
		expectedUploads = 1
	}
	if count != expectedUploads {
		t.Fatal("shared reading changed original upload", count)
	}
	if !managed && (readerIO == 0 || producerIO == 0) {
		t.Fatalf("shared unmanaged source skipped an actual actor's ACL: reader=%d producer=%d", readerIO, producerIO)
	}
}
