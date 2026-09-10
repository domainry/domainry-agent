package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type businessRelationSourceFixture struct {
	businessSourceFixture
	malformed string
}

func (f *businessRelationSourceFixture) BusinessCatalog(_ context.Context, q agentsdk.ConversationBusinessCatalogQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessCatalogPage, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessCatalogPage{}, err
	}
	if q.Kind != "relations" || q.ObjectKey != "customer" {
		return agentsdk.ConversationBusinessCatalogPage{}, fmt.Errorf("invalid relation discovery reached source")
	}
	return agentsdk.ConversationBusinessCatalogPage{Items: []agentsdk.ConversationBusinessObject{}, Relations: []agentsdk.ConversationBusinessRelation{{Key: "actual-orders-relation", Direction: "reverse", ObjectKey: "order"}}, Complete: true}, nil
}

func (f *businessRelationSourceFixture) QueryRelatedBusinessRecords(_ context.Context, q agentsdk.ConversationBusinessRelatedQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRelatedPage, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessRelatedPage{}, err
	}
	if q.ObjectKey != "customer" || q.RecordID != "customer-2" || q.RelationKey != "actual-orders-relation" || q.PageSize != 1 {
		return agentsdk.ConversationBusinessRelatedPage{}, fmt.Errorf("invalid relationship reached source")
	}
	page := 1
	if q.Cursor != "" {
		if q.Cursor != "actual-relation-page-2" {
			return agentsdk.ConversationBusinessRelatedPage{}, fmt.Errorf("invented relation cursor")
		}
		page = 2
	}
	out := agentsdk.ConversationBusinessRelatedPage{SourceObjectKey: q.ObjectKey, SourceRecordID: q.RecordID, RelationKey: q.RelationKey, ConversationBusinessRecordPage: agentsdk.ConversationBusinessRecordPage{
		ObjectKey: "order", Page: page, PageSize: 1, Items: []agentsdk.ConversationBusinessRecord{{ID: fmt.Sprintf("order-%d", page), Data: map[string]json.RawMessage{"name": json.RawMessage(fmt.Sprintf(`"关联订单%d"`, page))}}},
	}}
	if page == 1 {
		out.HasNext = true
		out.NextCursor = "actual-relation-page-2"
	}
	switch f.malformed {
	case "source":
		out.SourceRecordID = "OTHER-SOURCE"
	case "field":
		out.Items[0].Data["secret"] = json.RawMessage(`"PRIVATE-RELATION-RESULT"`)
	case "cursor":
		out.NextCursor = ""
	}
	return out, nil
}

func (f *businessRelationSourceFixture) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	var value any
	var err error
	if e.Operation == "business_catalog" {
		var q agentsdk.ConversationBusinessCatalogQuery
		if err := json.Unmarshal(e.Input, &q); err != nil {
			return err
		}
		value, err = f.BusinessCatalog(ctx, q, a)
	} else if e.Operation == "query_related_records" {
		var q agentsdk.ConversationBusinessRelatedQuery
		if err := json.Unmarshal(e.Input, &q); err != nil {
			return err
		}
		value, err = f.QueryRelatedBusinessRecords(ctx, q, a)
	} else {
		return fmt.Errorf("unrecognized relation evidence")
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(mustJSON(value), e.Data) {
		return fmt.Errorf("relation evidence changed")
	}
	return nil
}

func TestBusinessRelationToolTraversesAndReauthorizesThroughSaaS(t *testing.T) {
	for _, malformed := range []string{"", "source", "field", "cursor"} {
		t.Run("response="+malformed, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			source := &businessRelationSourceFixture{malformed: malformed}
			policy := personalReadAuthorizer{}
			host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				last := in.Messages[len(in.Messages)-1].Content
				switch n {
				case 1:
					return resultToolCall("query_related_records", "reject-recursion", map[string]any{"object_key": "customer", "record_id": "customer-2", "relation_key": "actual-orders-relation", "depth": 3}), nil
				case 2:
					if !strings.Contains(last, "arguments_invalid") {
						t.Error("recursive expansion accepted")
					}
					return resultToolCall("business_catalog", "relations", map[string]any{"kind": "relations", "object_key": "customer"}), nil
				case 3:
					if !strings.Contains(last, "actual-orders-relation") {
						t.Error("relation catalog not supplied")
					}
					return resultToolCall("query_related_records", "first", map[string]any{"object_key": "customer", "record_id": "customer-2", "relation_key": "actual-orders-relation", "fields": []string{"name"}, "page_size": 1}), nil
				case 4:
					if malformed != "" {
						if !strings.Contains(last, "business_response_invalid") || strings.Contains(last, "PRIVATE-") || strings.Contains(last, "OTHER-SOURCE") {
							t.Error("malformed result disclosed", last)
						}
						return (&executionModel{}).answerResult(), nil
					}
					if !strings.Contains(last, "actual-relation-page-2") {
						t.Error("relation cursor missing")
					}
					return resultToolCall("query_related_records", "next", map[string]any{"object_key": "customer", "record_id": "customer-2", "relation_key": "actual-orders-relation", "fields": []string{"name"}, "page_size": 1, "cursor": "actual-relation-page-2"}), nil
				default:
					if !strings.Contains(last, "关联订单2") {
						t.Error("second relation page missing")
					}
					return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "查到关联订单1和关联订单2。"}, FinishReason: "stop"}, nil
				}
			}}
			service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			server, err := agentserver.New(agentserver.Config{APIKey: "relation-test", Conversations: service, ConversationRuntimeID: a.RuntimeID})
			if err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(server.Handler())
			defer upstream.Close()
			binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "relation-test", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			api := binding.(agentsdk.ConversationBinding).Conversations()
			c, err := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "relations"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := api.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "查询客户的关联订单"}, a)
			if err != nil {
				t.Fatal(err)
			}
			done := waitConversation(t, api, c.ID, run.ID)
			if done.Status != "completed" || strings.Contains(string(mustJSON(done)), "PRIVATE-RELATION-RESULT") || strings.Contains(string(mustJSON(done)), "OTHER-SOURCE") {
				t.Fatal(done)
			}
			if malformed != "" {
				return
			}
			for _, revoked := range []bool{false, true, false} {
				source.revoked.Store(revoked)
				page, err := api.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
				if err != nil || len(page.Items) != 2 {
					t.Fatal(page, err)
				}
				if revoked == strings.Contains(page.Items[1].Content, "关联订单2") || revoked != (page.Items[1].AccessError != "") {
					t.Fatal("historical relation permissions ignored", page)
				}
			}
		})
	}
}

func TestBusinessRelationToolIsAbsentWithoutHostExtension(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy := personalReadAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		for _, tool := range in.Tools {
			if tool.Key == "query_related_records" {
				t.Error("unsupported relation tool published")
			}
		}
		return (&executionModel{}).answerResult(), nil
	}}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: &businessSourceFixture{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "no-relations"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "查询"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitConversation(t, service, c.ID, run.ID); result.Status != "completed" {
		t.Fatal(result)
	}
}
