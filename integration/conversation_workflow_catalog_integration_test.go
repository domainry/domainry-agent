package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type workflowCatalogSource struct {
	businessSourceFixture
	overbroad bool
}

func (f *workflowCatalogSource) BusinessCatalog(_ context.Context, q agentsdk.ConversationBusinessCatalogQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessCatalogPage, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessCatalogPage{}, err
	}
	if q.Kind != "workflows" || q.ObjectKey != "" || q.WorkflowKey != "" && q.After != "" {
		return agentsdk.ConversationBusinessCatalogPage{}, fmt.Errorf("invalid selector reached host")
	}
	item := agentsdk.ConversationBusinessOperation{Key: "global.review", Label: "全局审核", Version: "published-1"}
	if q.WorkflowKey != "" {
		if q.WorkflowKey != item.Key {
			return agentsdk.ConversationBusinessCatalogPage{}, &agentsdk.Error{Class: "forbidden"}
		}
		item.InputSchema = json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"],"additionalProperties":false}`)
	}
	out := agentsdk.ConversationBusinessCatalogPage{Items: []agentsdk.ConversationBusinessObject{}, Workflows: []agentsdk.ConversationBusinessOperation{item}, Complete: true}
	if f.overbroad {
		out.Items = []agentsdk.ConversationBusinessObject{{Key: "UNREQUESTED-OBJECT", Version: "1"}}
	}
	return out, nil
}

func (f *workflowCatalogSource) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	var q agentsdk.ConversationBusinessCatalogQuery
	if err := json.Unmarshal(e.Input, &q); err != nil {
		return err
	}
	current, err := f.BusinessCatalog(ctx, q, a)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(current)
	if err != nil || !bytes.Equal(raw, e.Data) {
		return fmt.Errorf("saved workflow catalog changed")
	}
	return nil
}

func TestWorkflowCatalogDiscoveryPersistsAndReauthorizesWithoutExecution(t *testing.T) {
	for _, overbroad := range []bool{false, true} {
		t.Run(fmt.Sprint("overbroad=", overbroad), func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			policy := personalReadAuthorizer{}
			host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			source := &workflowCatalogSource{overbroad: overbroad}
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				switch n {
				case 1:
					return resultToolCall("business_catalog", "invalid-selector", map[string]any{"kind": "workflows", "action_key": "customer.submit"}), nil
				case 2:
					if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "arguments_invalid") {
						t.Error("mixed selector was not rejected")
					}
					return resultToolCall("business_catalog", "list-workflows", map[string]any{"kind": "workflows", "limit": 1}), nil
				case 3:
					last := in.Messages[len(in.Messages)-1].Content
					if overbroad {
						if !strings.Contains(last, "business_response_invalid") || strings.Contains(last, "UNREQUESTED-OBJECT") {
							t.Error("mixed host response reached model")
						}
						return (&executionModel{}).answerResult(), nil
					}
					if !strings.Contains(last, "global.review") {
						t.Error("workflow discovery absent from model input")
					}
					return resultToolCall("business_catalog", "workflow-input", map[string]any{"kind": "workflows", "workflow_key": "global.review"}), nil
				default:
					if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "reason") {
						t.Error("published workflow payload missing")
					}
					return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "全局审核需要填写 reason；本次只查看了流程参数。"}, FinishReason: "stop"}, nil
				}
			}}
			service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "workflow-catalog"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "discover", Message: "查看全局审核流程需要什么参数"}, a)
			if err != nil {
				t.Fatal(err)
			}
			done := waitConversation(t, service, c.ID, run.ID)
			if done.Status != "completed" {
				t.Fatal(done)
			}
			if overbroad {
				if strings.Contains(string(mustJSON(done)), "UNREQUESTED-OBJECT") {
					t.Fatal("mixed response entered public ledger")
				}
				return
			}
			messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(messages.Items) != 2 || !strings.Contains(messages.Items[1].Content, "reason") {
				t.Fatal(messages, err)
			}
			source.revoked.Store(true)
			messages, err = service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || messages.Items[1].AccessError == "" || strings.Contains(messages.Items[1].Content, "reason") {
				t.Fatal("revoked workflow schema retained", messages, err)
			}
		})
	}
}
