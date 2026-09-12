package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type extractionSourceFixture struct {
	current agentsdk.ConversationKnowledgeResult
	revoke  func()
}

func (f *extractionSourceFixture) SearchKnowledge(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return f.current, nil
}
func (f *extractionSourceFixture) ReadKnowledge(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return f.current, nil
}
func (f *extractionSourceFixture) RevalidateKnowledge(_ context.Context, saved agentsdk.ConversationKnowledgeResult, _ agentsdk.ConversationAuthority) error {
	if f.revoke != nil {
		f.revoke()
	}
	if conversationDigest(saved) != conversationDigest(f.current) {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	return nil
}
func (f *extractionSourceFixture) KnowledgeExtractionPassages(_ context.Context, saved agentsdk.ConversationKnowledgeResult, _ agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	var content struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(saved.Data, &content) != nil {
		return nil, errors.New("invalid content")
	}
	return []agentsdk.KnowledgeDocumentPassage{{DocumentID: saved.DocumentID, Title: "来源", Content: content.Text}}, nil
}

type extractionAuthorizationFixture struct{ allowed bool }

func (f *extractionAuthorizationFixture) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: f.allowed}, nil
}

func TestExtractionResultCannotChangeValueEvidenceSchemaOrSource(t *testing.T) {
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "r", WorkspaceID: "w", UserID: "a"}
	source := &extractionSourceFixture{current: agentsdk.ConversationKnowledgeResult{Provider: "fixture", KBID: "kb", Operation: "fetch", DocumentID: "document", ScopeSHA256: "receipt", Data: json.RawMessage(`{"text":"客户：青禾公司\n金额：123.45 元"}`)}}
	args := agentsdk.KnowledgeExtractionArguments{DocumentID: "document", KnowledgeExtractionPlan: agentsdk.KnowledgeExtractionPlan{Fields: []agentsdk.KnowledgeExtractionField{{KnowledgeExtractionColumn: agentsdk.KnowledgeExtractionColumn{Key: "amount", Type: "decimal", Required: true}, Pattern: `金额：([0-9.]+)`}}}}
	raw, _ := json.Marshal(args)
	request := agentsdk.ConversationToolRequest{Authority: a, Call: agentsdk.ConversationToolCall{Name: "knowledge_extract", Arguments: string(raw)}, Definition: agentsdk.KnowledgeExtractionTool()}
	policy := &extractionAuthorizationFixture{allowed: true}
	host := &knowledgeConversationHost{source: source, authorizer: policy}
	result, err := host.InvokeConversationTool(t.Context(), request)
	if err != nil || result.Status != "completed" {
		t.Fatal("extraction invocation", result.ErrorCode, err)
	}
	if err = host.AuthorizeConversationToolResult(t.Context(), request, result); err != nil {
		t.Fatal("valid result refused", err)
	}
	legacy := request
	legacy.Definition = agentsdk.LegacyKnowledgeExtractionTool()
	if !knownKnowledgeDefinition(legacy.Definition) {
		t.Fatal("legacy extraction definition lost")
	}
	if err = host.AuthorizeConversationToolResult(t.Context(), legacy, result); err != nil {
		t.Fatal("legacy document receipt refused", err)
	}
	for _, definition := range agentsdk.HistoricalRemoteKnowledgeTools() {
		if definition.Key != "knowledge_extract" {
			continue
		}
		remote := request
		remote.Definition = definition
		if err := host.AuthorizeConversationToolResult(t.Context(), remote, result); err != nil {
			t.Fatal("historical remote receipt refused", definition.Version, err)
		}
		remote.Definition.Description += " altered"
		if err := host.AuthorizeConversationToolResult(t.Context(), remote, result); err == nil {
			t.Fatal("changed historical definition accepted")
		}
	}
	legacy.Call.Arguments = `{"attachment_id":"att_0123456789abcdef0123456789abcdef","fields":[{"key":"amount","type":"decimal","pattern":"([0-9.]+)"}]}`
	if err = host.AuthorizeConversationToolResult(t.Context(), legacy, result); err == nil {
		t.Fatal("legacy definition accepted private attachment locator")
	}
	for name, alter := range map[string]func(*agentsdk.KnowledgeExtractionResult){
		"value":    func(v *agentsdk.KnowledgeExtractionResult) { value := "999"; v.Data.Fields[0].Value = &value },
		"quote":    func(v *agentsdk.KnowledgeExtractionResult) { v.Data.Fields[0].Evidence[0].Quote = "999" },
		"offset":   func(v *agentsdk.KnowledgeExtractionResult) { v.Data.Fields[0].Evidence[0].Start = 0 },
		"citation": func(v *agentsdk.KnowledgeExtractionResult) { v.Citations[0].Excerpt = "forged" },
		"complete": func(v *agentsdk.KnowledgeExtractionResult) { v.Data.Coverage.OriginalComplete = true },
		"schema":   func(v *agentsdk.KnowledgeExtractionResult) { v.Data.Fields[0].Type = "integer" },
		"source":   func(v *agentsdk.KnowledgeExtractionResult) { v.Source.DocumentID = "other-document" },
		"location": func(v *agentsdk.KnowledgeExtractionResult) {
			v.Data.Fields[0].Evidence[0].Location = &agentsdk.DocumentLocation{Sheet: "forged", Row: 1, Cell: "A1"}
		},
		"citation-location": func(v *agentsdk.KnowledgeExtractionResult) {
			v.Citations[0].Location = &agentsdk.DocumentLocation{Sheet: "forged", Row: 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := result
			var value agentsdk.KnowledgeExtractionResult
			json.Unmarshal(result.Content, &value)
			alter(&value)
			changed.Content, _ = json.Marshal(value)
			if e := host.AuthorizeConversationToolResult(t.Context(), request, changed); e == nil {
				t.Fatal("tampered extraction authorized")
			}
		})
	}
	changed := result
	var unknown map[string]any
	json.Unmarshal(result.Content, &unknown)
	unknown["page"] = 1
	changed.Content, _ = json.Marshal(unknown)
	if err = host.AuthorizeConversationToolResult(t.Context(), request, changed); err == nil {
		t.Fatal("fabricated physical page accepted")
	}
	source.current.Data = json.RawMessage(`{"text":"金额：999 元"}`)
	if err = host.AuthorizeConversationToolResult(t.Context(), request, result); err == nil {
		t.Fatal("changed remote content retained old result")
	}
}

func TestExtractionRechecksActionAfterReadAndPreservesCancellation(t *testing.T) {
	policy := &extractionAuthorizationFixture{allowed: true}
	source := &extractionSourceFixture{current: agentsdk.ConversationKnowledgeResult{Provider: "fixture", KBID: "kb", Operation: "fetch", DocumentID: "doc", ScopeSHA256: "receipt", Data: json.RawMessage(`{"text":"金额：123.45"}`)}, revoke: func() { policy.allowed = false }}
	args := agentsdk.KnowledgeExtractionArguments{DocumentID: "doc", KnowledgeExtractionPlan: agentsdk.KnowledgeExtractionPlan{Fields: []agentsdk.KnowledgeExtractionField{{KnowledgeExtractionColumn: agentsdk.KnowledgeExtractionColumn{Key: "amount", Type: "decimal"}, Pattern: `金额：([0-9.]+)`}}}}
	raw, _ := json.Marshal(args)
	request := agentsdk.ConversationToolRequest{Authority: agentsdk.ConversationAuthority{Known: true}, Call: agentsdk.ConversationToolCall{Name: "knowledge_extract", Arguments: string(raw)}, Definition: agentsdk.KnowledgeExtractionTool()}
	host := &knowledgeConversationHost{source: source, authorizer: policy}
	result, err := host.InvokeConversationTool(t.Context(), request)
	if err != nil || result.Status != "failed" || result.ErrorCode != "tool_access_denied" || string(result.Content) != `{"error":"tool_access_denied"}` {
		t.Fatal("late action revocation disclosed extraction", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = buildKnowledgeExtraction(ctx, args, source.current, []agentsdk.KnowledgeDocumentPassage{{DocumentID: "doc", Content: "金额：123.45"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation changed into a successful/ordinary result", err)
	}
}
