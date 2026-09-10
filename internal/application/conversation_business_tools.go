package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type businessConversationHost struct {
	base       agentsdk.ConversationToolHost
	authorizer agentsdk.ConversationToolAuthorizer
	source     agentsdk.ConversationBusinessSource
	repo       persistence.ConversationRepository
}

func businessTool(key string) (agentsdk.ConversationToolDefinition, bool) {
	definitions := append(agentsdk.BusinessConversationTools(), agentsdk.BusinessRelationConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessWorkflowConversationTools()...)
	for _, definition := range append(definitions, agentsdk.BusinessActionConversationTools()...) {
		if definition.Key == key {
			return definition, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}

func (h *businessConversationHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	tools, err := h.base.ConversationTools(ctx, a)
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if _, reserved := businessTool(tool.Key); reserved {
			return nil, fmt.Errorf("business tools are already registered by the host")
		}
	}
	tools = append([]agentsdk.ConversationToolDefinition(nil), tools...)
	definitions := agentsdk.BusinessConversationTools()
	if _, ok := h.source.(agentsdk.ConversationBusinessRelationSource); ok {
		definitions = append(definitions, agentsdk.BusinessRelationConversationTools()...)
	}
	if _, ok := h.source.(agentsdk.ConversationBusinessActionSource); ok {
		definitions = append(definitions, agentsdk.BusinessActionConversationTools()...)
	}
	if _, ok := h.source.(agentsdk.ConversationBusinessWorkflowSource); ok {
		definitions = append(definitions, agentsdk.BusinessWorkflowConversationTools()...)
	}
	for _, definition := range definitions {
		auth, err := h.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
		if err != nil {
			return nil, err
		}
		if auth.Granted && (!auth.ConfirmationRequired || definition.Effect == "write") {
			tools = append(tools, definition)
		}
	}
	return tools, nil
}

func (h *businessConversationHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	definition, business := businessTool(in.Definition.Key)
	if !business {
		return h.base.AuthorizeConversationTool(ctx, in)
	}
	if definition.Key == "query_related_records" {
		if _, ok := h.source.(agentsdk.ConversationBusinessRelationSource); !ok {
			return agentsdk.ConversationToolAuthorization{}, nil
		}
	}
	if !in.Authority.Known || conversationDigest(definition) != conversationDigest(in.Definition) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if definition.Key == "invoke_action" {
		return h.authorizeBusinessActionTool(ctx, in)
	}
	if definition.Key == "workflow_start" || definition.Key == "workflow_get" {
		if _, ok := h.source.(agentsdk.ConversationBusinessWorkflowSource); !ok {
			return agentsdk.ConversationToolAuthorization{}, nil
		}
		if definition.Key == "workflow_start" {
			return h.authorizeBusinessWorkflowTool(ctx, in)
		}
	}
	if in.ConversationID != "" {
		if _, err := h.repo.Get(ctx, in.ConversationID, in.Authority); err != nil {
			return agentsdk.ConversationToolAuthorization{}, err
		}
	}
	return h.authorizer.AuthorizeConversationTool(ctx, in)
}

func businessFailureCode(err error) string {
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		switch coded.Class {
		case "forbidden":
			return "business_access_denied"
		case "not_found":
			return "business_record_not_found"
		case "bad_request":
			return "business_request_invalid"
		case "conflict":
			return "business_source_changed"
		}
	}
	return "business_unavailable"
}

func businessEvidenceScope(source string, a agentsdk.ConversationAuthority) string {
	return conversationDigest([]string{source, a.RuntimeID, a.WorkspaceID, a.UserID})
}

func validBusinessRecords(records []agentsdk.ConversationBusinessRecord, fields []string) bool {
	selected := map[string]bool{}
	for _, field := range fields {
		selected[field] = true
	}
	ids := map[string]bool{}
	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" || len(record.ID) > 256 || ids[record.ID] || record.Data == nil {
			return false
		}
		ids[record.ID] = true
		for field, value := range record.Data {
			if len(fields) > 0 && !selected[field] || !json.Valid(value) {
				return false
			}
		}
	}
	return true
}

func (h *businessConversationHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	definition, business := businessTool(in.Definition.Key)
	if !business {
		return h.base.InvokeConversationTool(ctx, in)
	}
	if definition.Key == "invoke_action" {
		return h.invokeBusinessActionTool(ctx, in, false)
	}
	if definition.Key == "workflow_start" || definition.Key == "workflow_get" {
		return h.invokeBusinessWorkflowTool(ctx, in, false)
	}
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil || !auth.Granted || auth.ConfirmationRequired || in.Call.Name != definition.Key {
		return personalToolFailure("tool_access_denied"), nil
	}
	schema, err := compileConversationSchema(definition.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	source := h.source.BusinessSourceIdentity()
	if strings.TrimSpace(source) == "" || len(source) > 2048 {
		return personalToolFailure("business_unavailable"), nil
	}
	var output any
	switch definition.Key {
	case "business_catalog":
		var query agentsdk.ConversationBusinessCatalogQuery
		_ = json.Unmarshal([]byte(in.Call.Arguments), &query)
		if query.Limit == 0 {
			query.Limit = 10
		}
		if !query.ValidSelector() {
			return personalToolFailure("arguments_invalid"), nil
		}

		var page agentsdk.ConversationBusinessCatalogPage
		page, err = h.source.BusinessCatalog(ctx, query, in.Authority)
		if err == nil && (len(page.Items)+len(page.Actions)+len(page.Workflows)+len(page.Relations) > query.Limit || len(page.NextCursor) > 2048 || page.Complete && page.NextCursor != "" || !page.Complete && page.NextCursor == "" || (query.Kind == "workflows" || query.Kind == "actions" || query.Kind == "relations") && len(page.Items) > 0 || query.Kind != "workflows" && len(page.Workflows) > 0 || query.Kind != "actions" && len(page.Actions) > 0 || query.Kind != "relations" && len(page.Relations) > 0) {
			return personalToolFailure("business_response_invalid"), nil
		}
		if err == nil && (query.Kind == "" || query.Kind == "objects") && query.ObjectKey != "" && (len(page.Items) != 1 || page.Items[0].Key != query.ObjectKey) {
			return personalToolFailure("business_response_invalid"), nil
		}
		if err == nil && query.ActionKey != "" && (len(page.Actions) != 1 || page.Actions[0].Key != query.ActionKey) {
			return personalToolFailure("business_response_invalid"), nil
		}
		if err == nil && query.WorkflowKey != "" && (len(page.Workflows) != 1 || page.Workflows[0].Key != query.WorkflowKey) {
			return personalToolFailure("business_response_invalid"), nil
		}
		output = page
	case "query_records":
		var query agentsdk.ConversationBusinessQuery
		_ = json.Unmarshal([]byte(in.Call.Arguments), &query)
		if query.Page == 0 && query.Cursor == "" {
			query.Page = 1
		}
		if query.PageSize == 0 {
			query.PageSize = 20
		}
		var page agentsdk.ConversationBusinessRecordPage
		page, err = h.source.QueryBusinessRecords(ctx, query, in.Authority)
		if err == nil && (page.ObjectKey != query.ObjectKey || query.Page != 0 && page.Page != query.Page || page.Page < 1 || page.Page > 1000000 || page.PageSize != query.PageSize || len(page.Items) > query.PageSize || len(page.NextCursor) > 16384 || !page.HasNext && page.NextCursor != "" || page.Total != nil && *page.Total < 0 || !validBusinessRecords(page.Items, query.Fields)) {
			return personalToolFailure("business_response_invalid"), nil
		}
		output = page
	case "query_related_records":
		var query agentsdk.ConversationBusinessRelatedQuery
		_ = json.Unmarshal([]byte(in.Call.Arguments), &query)
		if query.PageSize == 0 {
			query.PageSize = 20
		}
		var page agentsdk.ConversationBusinessRelatedPage
		page, err = h.source.(agentsdk.ConversationBusinessRelationSource).QueryRelatedBusinessRecords(ctx, query, in.Authority)
		if err == nil && (page.SourceObjectKey != query.ObjectKey || page.SourceRecordID != query.RecordID || page.RelationKey != query.RelationKey || page.ObjectKey == "" || page.Page < 1 || page.Page > 1000000 || page.PageSize != query.PageSize || len(page.Items) > query.PageSize || len(page.NextCursor) > 16384 || !page.HasNext && page.NextCursor != "" || page.HasNext && (page.NextCursor == "" || len(page.Items) == 0) || page.Total != nil && *page.Total < 0 || !validBusinessRecords(page.Items, query.Fields)) {
			return personalToolFailure("business_response_invalid"), nil
		}
		output = page
	case "get_record":
		var query agentsdk.ConversationBusinessGet
		_ = json.Unmarshal([]byte(in.Call.Arguments), &query)
		var record agentsdk.ConversationBusinessRecord
		record, err = h.source.GetBusinessRecord(ctx, query, in.Authority)
		if err == nil && (record.ID != query.RecordID || !validBusinessRecords([]agentsdk.ConversationBusinessRecord{record}, query.Fields)) {
			return personalToolFailure("business_response_invalid"), nil
		}
		output = record
	}
	if err != nil {
		return personalToolFailure(businessFailureCode(err)), nil
	}
	data, err := json.Marshal(output)
	if err != nil {
		return personalToolFailure("business_response_invalid"), nil
	}
	if source != h.source.BusinessSourceIdentity() {
		return personalToolFailure("business_source_changed"), nil
	}
	evidence := agentsdk.ConversationBusinessEvidence{Version: 1, Source: source, ScopeSHA256: businessEvidenceScope(source, in.Authority), Operation: definition.Key, Input: json.RawMessage(in.Call.Arguments), Data: data}
	if sealer, ok := h.source.(agentsdk.ConversationBusinessEvidenceSealer); ok {
		evidence.HostProof, err = sealer.SealBusinessEvidence(ctx, evidence, in.Authority)
		if err != nil {
			return personalToolFailure(businessFailureCode(err)), nil
		}
		if len(evidence.HostProof) > 4096 {
			return personalToolFailure("business_response_invalid"), nil
		}
		if source != h.source.BusinessSourceIdentity() {
			return personalToolFailure("business_source_changed"), nil
		}
	}
	return personalToolResult(evidence)
}

func (h *businessConversationHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if in.Definition.Key == "invoke_action" {
		return h.invokeBusinessActionTool(ctx, in, true)
	}
	if in.Definition.Key == "workflow_start" {
		return h.invokeBusinessWorkflowTool(ctx, in, true)
	}
	if _, business := businessTool(in.Definition.Key); business {
		return h.InvokeConversationTool(ctx, in)
	}
	return h.base.ReconcileConversationTool(ctx, in)
}

func (h *businessConversationHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, in agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	if authorizer, ok := h.base.(agentsdk.ConversationInteractionAuthorizer); ok {
		return authorizer.AuthorizeConversationInteraction(ctx, a, in)
	}
	return agentsdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}

func (h *businessConversationHost) AuthorizeConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if _, business := businessTool(in.Definition.Key); !business {
		if authorizer, ok := h.base.(agentsdk.ConversationToolResultAuthorizer); ok {
			return authorizer.AuthorizeConversationToolResult(ctx, in, result)
		}
		return nil
	}
	var evidence agentsdk.ConversationBusinessEvidence
	source := h.source.BusinessSourceIdentity()
	if json.Unmarshal(result.Content, &evidence) != nil || evidence.Version != 1 || evidence.Operation != in.Call.Name || evidence.Source != source || evidence.ScopeSHA256 != businessEvidenceScope(source, in.Authority) || conversationDigest(evidence.Input) != conversationDigest(json.RawMessage(in.Call.Arguments)) || !json.Valid(evidence.Data) {
		return conversationFailure("conflict", "business_response_invalid")
	}
	var err error
	if in.Call.Name == "invoke_action" {
		source, ok := h.source.(agentsdk.ConversationBusinessActionSource)
		if !ok {
			return conversationFailure("forbidden", "business_access_denied")
		}
		err = source.RevalidateBusinessAction(ctx, evidence, in.Authority)
	} else if in.Call.Name == "workflow_start" || in.Call.Name == "workflow_get" {
		source, ok := h.source.(agentsdk.ConversationBusinessWorkflowSource)
		if !ok {
			return conversationFailure("forbidden", "business_access_denied")
		}
		err = source.RevalidateBusinessWorkflow(ctx, evidence, in.Authority)
	} else {
		err = h.source.RevalidateBusiness(ctx, evidence, in.Authority)
	}
	if err != nil {
		return conversationFailure("forbidden", businessFailureCode(err))
	}
	return nil
}
