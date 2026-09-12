package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type businessSourceFixture struct {
	revoked      atomic.Bool
	catalogCalls atomic.Int32
}

type overbroadBusinessSource struct{ businessSourceFixture }

type cursorBusinessSource struct{ businessSourceFixture }

func (f *cursorBusinessSource) QueryBusinessRecords(_ context.Context, q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecordPage, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessRecordPage{}, err
	}
	page := 1
	if q.Cursor != "" {
		if q.Cursor != "fixture-page-2" || q.Page != 0 {
			return agentsdk.ConversationBusinessRecordPage{}, fmt.Errorf("cursor must determine page when page is omitted")
		}
		page = 2
	}
	out := agentsdk.ConversationBusinessRecordPage{ObjectKey: q.ObjectKey, Page: page, PageSize: 1, Items: []agentsdk.ConversationBusinessRecord{{ID: fmt.Sprintf("record-%d", page), Data: map[string]json.RawMessage{"name": json.RawMessage(`"客户"`)}}}}
	if page == 1 {
		out.HasNext = true
		out.NextCursor = "fixture-page-2"
	}
	return out, nil
}
func (f *cursorBusinessSource) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	var q agentsdk.ConversationBusinessQuery
	if err := json.Unmarshal(e.Input, &q); err != nil {
		return err
	}
	current, err := f.QueryBusinessRecords(ctx, q, a)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(current)
	if err != nil || !bytes.Equal(raw, e.Data) {
		return fmt.Errorf("saved cursor page changed")
	}
	return nil
}

func TestBusinessCursorPaginationContinuesWithoutInventedPageNumbers(t *testing.T) {
	repo := conversationRepository(t)
	source := &cursorBusinessSource{}
	host, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch n {
		case 1:
			return resultToolCall("query_records", "first", map[string]any{"object_key": "customer", "page_size": 1}), nil
		case 2:
			if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "fixture-page-2") {
				t.Fatal("next cursor not exposed to model")
			}
			return resultToolCall("query_records", "next", map[string]any{"object_key": "customer", "page_size": 1, "cursor": "fixture-page-2"}), nil
		default:
			if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "record-2") {
				t.Error("second cursor page rejected")
			}
			return (&executionModel{}).answerResult(), nil
		}
	}}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: personalReadAuthorizer{}, Business: source})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "cursor-query"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "cursor-query", Message: "继续查完客户"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if result := waitConversation(t, service, c.ID, run.ID); result.Status != "completed" {
		t.Fatalf("cursor run=%+v", result)
	}
}

func (f *overbroadBusinessSource) GetBusinessRecord(ctx context.Context, q agentsdk.ConversationBusinessGet, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecord, error) {
	record, err := f.businessSourceFixture.GetBusinessRecord(ctx, q, a)
	if err == nil {
		record.Data["secret"] = json.RawMessage(`"UNREQUESTED-PRIVATE-FIELD"`)
	}
	return record, err
}

func (*businessSourceFixture) BusinessSourceIdentity() string { return "fixture-business-v1" }
func (f *businessSourceFixture) authorize(a agentsdk.ConversationAuthority) error {
	if f.revoked.Load() || a != conversationAuthority() {
		return &agentsdk.Error{Class: "forbidden", Code: "PRIVATE-HOST-POLICY"}
	}
	return nil
}
func (f *businessSourceFixture) BusinessCatalog(_ context.Context, q agentsdk.ConversationBusinessCatalogQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessCatalogPage, error) {
	f.catalogCalls.Add(1)
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessCatalogPage{}, err
	}
	if q.ObjectKey != "" && q.ObjectKey != "customer" {
		return agentsdk.ConversationBusinessCatalogPage{}, &agentsdk.Error{Class: "forbidden"}
	}
	object := agentsdk.ConversationBusinessObject{Key: "customer", Label: "客户", Version: "schema-1"}
	if q.ObjectKey != "" {
		object.Fields = []agentsdk.ConversationBusinessField{{Key: "name", Type: "text", FilterOperators: []string{"eq"}, Sortable: true}, {Key: "balance", Type: "integer"}}
	}
	return agentsdk.ConversationBusinessCatalogPage{Items: []agentsdk.ConversationBusinessObject{object}, Complete: true}, nil
}
func (f *businessSourceFixture) QueryBusinessRecords(ctx context.Context, q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecordPage, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessRecordPage{}, err
	}
	if q.ObjectKey != "customer" || q.Page != 2 || q.PageSize != 1 || len(q.Sort) != 1 || q.Sort[0].Field != "name" || q.Sort[0].Direction != "asc" {
		return agentsdk.ConversationBusinessRecordPage{}, &agentsdk.Error{Class: "bad_request"}
	}
	record, err := f.GetBusinessRecord(ctx, agentsdk.ConversationBusinessGet{ObjectKey: q.ObjectKey, RecordID: "customer-2", Fields: q.Fields}, a)
	if err != nil {
		return agentsdk.ConversationBusinessRecordPage{}, err
	}
	return agentsdk.ConversationBusinessRecordPage{ObjectKey: q.ObjectKey, Page: 2, PageSize: 1, Items: []agentsdk.ConversationBusinessRecord{record}, HasNext: false}, nil
}
func (f *businessSourceFixture) GetBusinessRecord(_ context.Context, q agentsdk.ConversationBusinessGet, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecord, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationBusinessRecord{}, err
	}
	if q.ObjectKey != "customer" || q.RecordID != "customer-2" {
		return agentsdk.ConversationBusinessRecord{}, &agentsdk.Error{Class: "not_found"}
	}
	data := map[string]json.RawMessage{"name": json.RawMessage(`"客户乙"`), "balance": json.RawMessage(`9007199254740993`)}
	selected := map[string]json.RawMessage{}
	for _, field := range q.Fields {
		value, ok := data[field]
		if !ok {
			return agentsdk.ConversationBusinessRecord{}, &agentsdk.Error{Class: "forbidden", Code: "PRIVATE-HOST-POLICY"}
		}
		selected[field] = value
	}
	return agentsdk.ConversationBusinessRecord{ID: q.RecordID, Version: "record-7", Data: selected}, nil
}
func (f *businessSourceFixture) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	if err := f.authorize(a); err != nil {
		return err
	}
	var current any
	var err error
	switch e.Operation {
	case "business_catalog":
		var q agentsdk.ConversationBusinessCatalogQuery
		_ = json.Unmarshal(e.Input, &q)
		current, err = f.BusinessCatalog(ctx, q, a)
	case "query_records":
		var q agentsdk.ConversationBusinessQuery
		_ = json.Unmarshal(e.Input, &q)
		current, err = f.QueryBusinessRecords(ctx, q, a)
	case "get_record":
		var q agentsdk.ConversationBusinessGet
		_ = json.Unmarshal(e.Input, &q)
		current, err = f.GetBusinessRecord(ctx, q, a)
	default:
		return &agentsdk.Error{Class: "forbidden"}
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(mustJSON(current), e.Data) {
		return &agentsdk.Error{Class: "conflict"}
	}
	return nil
}

func TestBusinessToolsPreserveScopePaginationAndEvidenceAcrossRestart(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	source := &businessSourceFixture{}
	policy := personalReadAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	frozen := make(chan []byte, 1)
	var expected []byte
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1].Content
		if strings.Contains(last, "PRIVATE-HOST-POLICY") {
			t.Error("host policy body leaked")
		}
		switch n {
		case 1:
			return resultToolCall("business_catalog", "bad-scope", map[string]any{"workspace_id": "other"}), nil
		case 2:
			if !strings.Contains(last, "arguments_invalid") || source.catalogCalls.Load() != 0 {
				t.Error("model-supplied scope reached host")
			}
			return resultToolCall("business_catalog", "catalog", map[string]any{}), nil
		case 3:
			if !strings.Contains(last, "customer") {
				t.Error("business object missing")
			}
			return resultToolCall("business_catalog", "fields", map[string]string{"object_key": "customer"}), nil
		case 4:
			if !strings.Contains(last, "balance") {
				t.Error("published fields missing")
			}
			return resultToolCall("query_records", "query", agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, Page: 2, PageSize: 1, Sort: []agentsdk.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}), nil
		case 5:
			if !strings.Contains(last, "9007199254740993") || !strings.Contains(last, `"page":2`) || !strings.Contains(last, `"has_next":false`) {
				t.Error("record precision or pagination lost")
			}
			return resultToolCall("get_record", "field-denied", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"secret"}}), nil
		case 6:
			if !strings.Contains(last, "business_access_denied") {
				t.Error("field denial missing")
			}
			return resultToolCall("get_record", "read", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"name", "balance"}}), nil
		case 7:
			if !strings.Contains(last, "客户乙") {
				t.Error("actual record missing")
			}
			frozen <- mustJSON(in)
			return agentsdk.ConversationStepResult{}, fmt.Errorf("model disconnected")
		case 8:
			if !bytes.Equal(expected, mustJSON(in)) {
				t.Error("recovery changed the frozen input")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "客户乙的已授权余额为 9007199254740993。"}, FinishReason: "stop"}, nil
		default:
			t.Error("unexpected model call")
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected")
		}
	}}
	options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "business-evidence"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "customer", Message: "查看第二页客户余额"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "failed" || done.ErrorCode != "provider_failed" {
		t.Fatalf("expected recoverable model interruption: %+v", done)
	}
	expected = <-frozen
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	source.revoked.Store(true)
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.ErrorCode != "business_access_denied" {
		t.Fatalf("revoked business evidence resumed: %+v", done)
	}
	source.revoked.Store(false)
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" {
		t.Fatalf("restored source failed: %+v", done)
	}
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !strings.Contains(page.Items[1].Content, "客户乙") {
		t.Fatalf("missing saved business answer: %+v", page)
	}
	source.revoked.Store(true)
	page, err = service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[1].AccessError == "" || strings.Contains(page.Items[1].Content, "客户乙") {
		t.Fatal("revoked business facts retained in history")
	}
	other := a
	other.UserID = "other"
	if _, err = service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, other); err == nil {
		t.Fatal("other owner read business history")
	}
}

func TestBusinessUnexpectedFieldsAreRejectedBeforeModel(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy := personalReadAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if n == 1 {
			return resultToolCall("get_record", "read", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"name"}}), nil
		}
		last := in.Messages[len(in.Messages)-1].Content
		if !strings.Contains(last, "business_response_invalid") || strings.Contains(last, "UNREQUESTED-PRIVATE-FIELD") {
			t.Error("unrequested field reached model or missing failure")
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "业务服务的返回字段与请求不符，本次未使用该结果。"}, FinishReason: "stop"}, nil
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: &overbroadBusinessSource{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "overbroad"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "读取客户名称"}, a)
	if err != nil {
		t.Fatal(err)
	}
	done := waitConversation(t, service, c.ID, run.ID)
	if done.Status != "completed" || len(done.Steps) != 2 || done.Steps[0].Calls[0].ErrorCode != "business_response_invalid" || strings.Contains(string(mustJSON(done)), "UNREQUESTED-PRIVATE-FIELD") {
		t.Fatalf("overbroad result retained: %+v", done)
	}
}

func TestBusinessSaaSReadAndRevocation(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy, source := personalReadAuthorizer{}, &businessSourceFixture{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch n {
		case 1:
			return resultToolCall("business_catalog", "catalog", map[string]string{"object_key": "customer"}), nil
		case 2:
			return resultToolCall("get_record", "read", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"name"}}), nil
		default:
			if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "客户乙") {
				t.Error("actual record missing from model input")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "客户名称为客户乙。"}, FinishReason: "stop"}, nil
		}
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "business-saas-test", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "business-saas-test", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	api := binding.(agentsdk.ConversationBinding).Conversations()
	c, err := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "business-saas"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := api.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "查询客户名称"}, a)
	if err != nil {
		t.Fatal(err)
	}
	done := waitConversation(t, api, c.ID, run.ID)
	if done.Status != "completed" || len(done.Steps) != 3 {
		t.Fatalf("business RPC lost execution: %+v", done)
	}
	page, err := api.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 2 || !strings.Contains(page.Items[1].Content, "客户乙") {
		t.Fatal("business reply did not cross SaaS boundary", err)
	}
	source.revoked.Store(true)
	page, err = api.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || page.Items[1].AccessError == "" || strings.Contains(page.Items[1].Content, "客户乙") {
		t.Fatal("SaaS history retained revoked data", err)
	}
	for _, dimension := range []string{"user", "workspace", "runtime"} {
		other := a
		switch dimension {
		case "user":
			other.UserID = "other"
		case "workspace":
			other.WorkspaceID = "other"
		case "runtime":
			other.RuntimeID = "other"
		}
		if _, err = api.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, other); err == nil {
			t.Fatalf("SaaS %s isolation failed", dimension)
		}
	}
}
