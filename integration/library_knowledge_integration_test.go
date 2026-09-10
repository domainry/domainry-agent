package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func TestLibraryKnowledgeToolsMembershipAndFrozenResume(t *testing.T) {
	repo := conversationRepository(t)
	libs := any(repo).(persistence.KnowledgeLibraryRepository)
	a := conversationAuthority()
	b := a
	b.UserID = "second"
	private, err := libs.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "private", Kind: "personal", Name: "A personal private"}, a)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := libs.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "shared", Kind: "shared", Name: "Shared project"}, a)
	if err != nil {
		t.Fatal(err)
	}
	shared, err = libs.SetKnowledgeLibraryMember(t.Context(), shared.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: shared.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requested := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args struct {
			KBID  string `json:"kb_id"`
			DocID string `json:"doc_id"`
			Query string `json:"query"`
		}
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			t.Error("invalid upstream request")
		}
		mu.Lock()
		requested = append(requested, args.KBID)
		mu.Unlock()
		text := "SHARED-LIBRARY-EVIDENCE"
		if args.KBID == "private" {
			text = "PRIVATE-LIBRARY-EVIDENCE"
		}
		if r.URL.Path == "/v1/kb/search" {
			fmt.Fprintf(w, `{"hits":[{"doc_id":"policy","title":"Policy","body":%q}]}`, text)
		} else {
			if args.DocID != "policy" {
				t.Error("wrong doc_id")
			}
			fmt.Fprintf(w, `{"doc_id":"policy","title":"Policy","body":%q}`, text)
		}
	}))
	defer upstream.Close()
	newSource := func(kb string) *provider.Knowledge {
		source, e := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-key", TeamID: "team", KBID: kb, WorkspaceID: a.WorkspaceID, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}})
		if e != nil {
			t.Fatal(e)
		}
		return source
	}
	policy := &libraryTestPolicy{}
	tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var frozen agentsdk.ConversationStepRequest
	var evidence agentsdk.ConversationKnowledgeResult
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if strings.Contains(string(mustJSON(in)), "PRIVATE-LIBRARY-EVIDENCE") {
			t.Error("private library leaked to model")
		}
		switch n {
		case 1:
			return resultToolCall("knowledge_libraries", "catalog", map[string]any{}), nil
		case 2:
			if !strings.Contains(last.Content, shared.ID) || strings.Contains(last.Content, private.ID) {
				t.Error("catalog not scoped by membership")
			}
			return resultToolCall("knowledge_search", "forged", map[string]any{"query": "policy", "library_id": private.ID}), nil
		case 3:
			if !strings.Contains(last.Content, "knowledge_access_denied") {
				t.Error("private library request not denied")
			}
			return resultToolCall("knowledge_search", "search", map[string]any{"query": "policy", "library_id": shared.ID}), nil
		case 4:
			if !strings.Contains(last.Content, "SHARED-LIBRARY-EVIDENCE") {
				t.Error("shared evidence missing")
			}
			return resultToolCall("knowledge_read", "read", map[string]any{"doc_id": "policy", "library_id": shared.ID}), nil
		case 5:
			var result agentsdk.ConversationToolResult
			if json.Unmarshal([]byte(last.Content), &result) != nil || json.Unmarshal(result.Content, &evidence) != nil || evidence.LibraryID != shared.ID || len(evidence.Citations) != 1 || evidence.Citations[0].LibraryID != shared.ID {
				t.Error("library provenance lost")
			}
			frozen = in
			return agentsdk.ConversationStepResult{}, fmt.Errorf("simulated model disconnect")
		case 6:
			if string(mustJSON(in)) != string(mustJSON(frozen)) {
				t.Error("frozen step was rewritten after membership restore")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "按共享资料处理。[[cite:" + evidence.Citations[0].ID + "]]"}, FinishReason: "stop"}, nil
		default:
			t.Error("revoked source reached model")
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected model invocation")
		}
	}}
	options := application.ConversationOptions{ToolHost: tools, PersonalAuthorizer: personalReadAuthorizer{}, LibraryAuthorizer: policy, LibraryKnowledge: []application.LibraryKnowledgeBinding{{WorkspaceID: a.WorkspaceID, LibraryID: private.ID, Source: newSource("private")}, {WorkspaceID: a.WorkspaceID, LibraryID: shared.ID, Source: newSource("shared")}}}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "library-test"}, b)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "question", Message: "查询有权限的资料"}, b)
	if err != nil {
		t.Fatal(err)
	}
	wait := func() agentsdk.ConversationRun {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			value, e := service.Run(t.Context(), c.ID, run.ID, b)
			if e != nil {
				t.Fatal(e)
			}
			if value.Terminal() {
				return value
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("library knowledge run timed out")
		return agentsdk.ConversationRun{}
	}
	if done := wait(); done.Status != "failed" || done.ErrorCode != "provider_failed" {
		t.Fatalf("expected recoverable model interruption: %+v", done)
	}
	service.Close()
	shared, err = libs.RemoveKnowledgeLibraryMember(t.Context(), shared.ID, b.UserID, shared.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := len(requested)
	mu.Unlock()
	if _, err = service.Resume(t.Context(), c.ID, run.ID, b); err != nil {
		t.Fatal(err)
	}
	if done := wait(); done.ErrorCode != "knowledge_access_denied" {
		t.Fatalf("removed member resumed: %+v", done)
	}
	mu.Lock()
	if len(requested) != before {
		t.Error("removed member reached remote source")
	}
	for _, kb := range requested {
		if kb != "shared" {
			t.Error("unauthorized remote KB queried", kb)
		}
	}
	mu.Unlock()
	shared, err = libs.SetKnowledgeLibraryMember(t.Context(), shared.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: shared.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Resume(t.Context(), c.ID, run.ID, b); err != nil {
		t.Fatal(err)
	}
	if done := wait(); done.Status != "completed" {
		t.Fatalf("restored reader cannot resume frozen data: %+v", done)
	}
	shared, err = libs.UpdateKnowledgeLibrary(t.Context(), shared.ID, agentsdk.KnowledgeLibraryUpdate{ExpectedRevision: shared.Revision, Name: shared.Name, Archived: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Messages(context.Background(), c.ID, agentsdk.ConversationMessageQuery{}, b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(view)), "按共享资料处理") || strings.Contains(string(mustJSON(view)), evidence.Citations[0].ID) {
		t.Fatal("archived library retained historical reply/citation")
	}
}
