package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type deliveryKnowledgeSource struct {
	extractionSourceFixture
	a      sdk.ConversationAuthority
	denied bool
	reads  int
}

func (s *deliveryKnowledgeSource) Search(context.Context, string, sdk.ConversationAuthority) (json.RawMessage, error) {
	return s.current.Data, nil
}
func (s *deliveryKnowledgeSource) AuthorizeKnowledgeResultRead(ctx context.Context, saved sdk.ConversationKnowledgeResult, a sdk.ConversationAuthority) error {
	s.reads++
	if s.denied || a != s.a {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	return s.RevalidateKnowledge(ctx, saved, a)
}
func (s *deliveryKnowledgeSource) SearchLibraryKnowledge(context.Context, string, string, sdk.ConversationAuthority) (sdk.ConversationKnowledgeResult, error) {
	return s.current, nil
}
func (s *deliveryKnowledgeSource) ReadLibraryKnowledge(context.Context, string, string, sdk.ConversationAuthority) (sdk.ConversationKnowledgeResult, error) {
	return s.current, nil
}
func (s *deliveryKnowledgeSource) ListKnowledgeLibraries(context.Context, string, int, sdk.ConversationAuthority) (sdk.ConversationKnowledgeResult, error) {
	return s.current, nil
}

type unsupportedDeliveryKnowledgeSource struct{ extractionSourceFixture }

func (s *unsupportedDeliveryKnowledgeSource) Search(context.Context, string, sdk.ConversationAuthority) (json.RawMessage, error) {
	return s.current.Data, nil
}

func TestDeliveryKnowledgeReadsDoNotAcquireProducerExecutionRights(t *testing.T) {
	for _, key := range []string{"knowledge_search", "knowledge_read", "knowledge_libraries", "knowledge_extract"} {
		t.Run(key, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			source := &deliveryKnowledgeSource{a: a, extractionSourceFixture: extractionSourceFixture{current: sdk.ConversationKnowledgeResult{Provider: "fixture", Operation: "fetch", DocumentID: "doc", ScopeSHA256: "exact", Data: json.RawMessage(`{"text":"金额：123.45"}`)}}}
			definition, _ := knowledgeTool(key)
			args := map[string]any{"doc_id": "doc"}
			switch key {
			case "knowledge_search":
				source.current.Operation = "search"
				source.current.Query = "金额"
				source.current.DocumentID = ""
				args = map[string]any{"query": "金额"}
			case "knowledge_libraries":
				source.current.Operation = "libraries"
				source.current.DocumentID = ""
				source.current.Query = `{"after":"","limit":0}`
				args = map[string]any{}
			}
			raw, _ := json.Marshal(source.current)
			if key == "knowledge_extract" {
				plan := sdk.KnowledgeExtractionArguments{DocumentID: "doc", KnowledgeExtractionPlan: sdk.KnowledgeExtractionPlan{Fields: []sdk.KnowledgeExtractionField{{KnowledgeExtractionColumn: sdk.KnowledgeExtractionColumn{Key: "amount", Type: "decimal", Required: true}, Pattern: `金额：([0-9.]+)`}}}}
				body, _ := json.Marshal(plan)
				_ = json.Unmarshal(body, &args)
				value, err := buildKnowledgeExtraction(t.Context(), plan, source.current, []sdk.KnowledgeDocumentPassage{{DocumentID: "doc", Title: "来源", Content: "金额：123.45"}})
				if err != nil {
					t.Fatal(err)
				}
				raw, _ = json.Marshal(value)
			}
			body, _ := json.Marshal(args)
			record := persistence.ConversationToolExecution{Definition: definition, Call: sdk.ConversationToolCall{ID: "original", Name: key, Arguments: string(body)}, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", Content: raw}}
			base := &deliveryReadTestHost{}
			availability := &deliveryArtifactPolicy{disabled: map[string]bool{}}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: privatePeerSources{}, options: ConversationOptions{Knowledge: source, ToolHost: &profileToolHost{base: base, allowed: map[string]bool{}}, PersonalAuthorizer: base, ToolAvailability: availability, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
			owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
			if _, err := s.sourceAudit(a).record(t.Context(), owner, record); err == nil || source.reads != 0 {
				t.Fatal("raw history used released-reading policy", err)
			}
			ctx := deliverySourceContext(context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{}), "released")
			checks := base.executionChecks
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err != nil || source.reads == 0 || base.executionChecks != checks {
				t.Fatalf("delivery required producer profile/action: %v", err)
			}
			source.denied = true
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil || base.executionChecks != checks {
				t.Fatal("owner denial fell back to execution", err)
			}
			source.denied = false
			availability.disabled[key] = true
			before := source.reads
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil || source.reads != before {
				t.Fatal("disabled tool result remained readable")
			}
			delete(availability.disabled, key)
			changed := record
			copy := *record.Result
			changed.Result = &copy
			copy.Content = json.RawMessage(strings.Replace(string(raw), "123.45", "999.99", 1))
			if key != "knowledge_libraries" {
				if _, err := s.sourceAudit(a).record(ctx, owner, changed); err == nil {
					t.Fatal("changed source/extracted value accepted")
				}
			}
			copy.Content = json.RawMessage(strings.TrimSuffix(string(raw), "}") + `,"unverified":"private"}`)
			if _, err := s.sourceAudit(a).record(ctx, owner, changed); err == nil {
				t.Fatal("unvalidated extra output field accepted")
			}
			if key == "knowledge_read" {
				copy.Content = raw
				changed.Call.Arguments = `{"attachment_id":"att_private"}`
				before = source.reads
				if _, err := s.sourceAudit(a).record(ctx, owner, changed); err == nil || source.reads != before {
					t.Fatal("private file entered independent source reader")
				}
			}
			s.options.Knowledge = &unsupportedDeliveryKnowledgeSource{extractionSourceFixture: source.extractionSourceFixture}
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
				t.Fatal("unsupported source bypassed old execution rights")
			}
		})
	}
}
