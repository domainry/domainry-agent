package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/domainry/domainry-tools/timeutil"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

type PersonalConversationHost struct {
	artifacts  *ConversationService
	knowledge  ConversationKnowledge
	repo       persistence.ConversationRepository
	history    persistence.ConversationHistoryRepository
	authorizer agentsdk.ConversationToolAuthorizer
	timezone   *time.Location
	now        func() time.Time
}

func NewPersonalConversationHost(repo persistence.ConversationRepository, authorizer agentsdk.ConversationToolAuthorizer, timezone string) (*PersonalConversationHost, error) {
	history, ok := repo.(persistence.ConversationHistoryRepository)
	if !ok || authorizer == nil {
		return nil, fmt.Errorf("personal tools require history persistence and a host authorizer")
	}
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid personal tool timezone")
	}
	return &PersonalConversationHost{repo: repo, history: history, authorizer: authorizer, timezone: location, now: time.Now}, nil
}

func (h *PersonalConversationHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	out := []agentsdk.ConversationToolDefinition{}
	for _, definition := range append(agentsdk.PersonalConversationTools(), agentsdk.ArtifactConversationTools()...) {
		if !h.supportsPersonalTool(definition) {
			continue
		}
		auth, err := h.authorizer.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
		if err != nil {
			return nil, err
		}
		if auth.Granted {
			if definition.Key == "artifact_edit" || definition.Key == "artifact_export" {
				read, _ := artifactTool("artifact_read")
				decision, err := h.authorizer.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: read})
				if err != nil {
					return nil, err
				}
				if !decision.Granted {
					continue
				}
			}
			out = append(out, definition)
		}
	}
	return out, nil
}

func (h *PersonalConversationHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	if !h.supportsPersonalTool(in.Definition) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	// The supplied definition is trusted only after matching source registration.
	known := false
	for _, definition := range append(agentsdk.PersonalConversationTools(), agentsdk.ArtifactConversationTools()...) {
		if conversationDigest(in.Definition) == conversationDigest(definition) {
			known = true
			break
		}
	}
	if !known || !in.Authority.Known {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if in.ConversationID != "" {
		if _, err := h.repo.Get(ctx, in.ConversationID, in.Authority); err != nil {
			return agentsdk.ConversationToolAuthorization{}, err
		}
	}
	auth, err := h.authorizer.AuthorizeConversationTool(ctx, in)
	if err == nil && auth.Granted && (in.Definition.Key == "artifact_edit" || in.Definition.Key == "artifact_export") {
		request := artifactReadAuthorizationRequest(in)
		readAuth, readErr := h.authorizer.AuthorizeConversationTool(ctx, request)
		if readErr != nil || !readAuth.Granted || readAuth.ConfirmationRequired {
			return agentsdk.ConversationToolAuthorization{}, readErr
		}
	}
	if err != nil || !auth.Granted || in.Definition.Effect != "write" || auth.ConfirmationRequired {
		return auth, err
	}
	// Identity permission permits access to this action. The separate, frozen
	// user scope or persisted confirmation permits this particular execution.
	auth.ConfirmationRequired = true
	if in.RunID == "" || in.ConversationID == "" {
		return auth, nil
	}
	run, err := h.repo.Run(ctx, in.ConversationID, in.RunID, in.Authority)
	if err != nil {
		return auth, err
	}
	if run.WriteScope.Allows(in.Definition.Key) {
		auth.ConfirmationRequired = false
		return auth, nil
	}
	if in.Confirmation == nil {
		return auth, nil
	}
	claim := personalToolClaim(in)
	record, found, err := h.repo.(persistence.ConversationInteractionRepository).ExecutionInteraction(ctx, claim, in.Step, in.Call.ID, "confirmation")
	if err != nil {
		return auth, err
	}
	i := record.Interaction
	receipt := confirmationReceipt(i, in.Authority)
	if found && receipt != nil && conversationDigest(receipt) == conversationDigest(in.Confirmation) && receipt.ID == in.ConfirmationID && i.DefinitionHash == conversationDigest(in.Definition) && i.ArgumentsHash == conversationDigest(in.Call.Arguments) {
		auth.ConfirmationRequired = false
	}
	return auth, nil
}

func personalToolClaim(in agentsdk.ConversationToolRequest) persistence.ConversationClaim {
	return persistence.ConversationClaim{Authority: in.Authority, Run: agentsdk.ConversationRun{ID: in.RunID, ConversationID: in.ConversationID}, Owner: in.LeaseOwner, Fence: in.Fence}
}

func (h *PersonalConversationHost) supportsPersonalTool(definition agentsdk.ConversationToolDefinition) bool {
	if strings.HasPrefix(definition.Key, "artifact_") {
		if h.artifacts == nil || h.artifacts.options.ArtifactStorage == nil {
			return false
		}
		if _, ok := h.repo.(persistence.ConversationArtifactRepository); !ok {
			return false
		}
		if _, ok := h.repo.(persistence.ConversationSourceRepository); !ok {
			return false
		}
		if definition.Effect == "write" {
			_, ok := h.repo.(persistence.ConversationArtifactMutationRepository)
			return ok && h.supportsConversationInteractions()
		}
		return true
	}
	if definition.Key == "execution_read" {
		_, ok := h.repo.(persistence.ConversationExecutionReadRepository)
		return ok
	}
	if definition.Key == "tool_result_read" {
		_, ok := h.repo.(persistence.ConversationResultRepository)
		return ok
	}
	if strings.HasPrefix(definition.Key, "todo_") {
		if _, ok := h.repo.(persistence.ConversationTodoRepository); !ok {
			return false
		}
	}
	if definition.Key == "ask_user" || definition.Effect == "write" {
		if !h.supportsConversationInteractions() {
			return false
		}
	}
	if definition.Effect == "write" {
		_, ok := h.repo.(persistence.ConversationPersonalMutationRepository)
		return ok
	}
	return true
}

func personalToolResult(value any) (agentsdk.ConversationToolResult, error) {
	raw, err := json.Marshal(value)
	return agentsdk.ConversationToolResult{Status: "completed", Content: raw}, err
}
func personalToolFailure(code string) agentsdk.ConversationToolResult {
	return agentsdk.ConversationToolResult{Status: "failed", ErrorCode: code, Content: json.RawMessage(conversationJSONText(map[string]string{"error": code}))}
}

func (h *PersonalConversationHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	// Host calls remain safe when invoked directly through the SDK: no caller
	// can bypass policy or argument validation by skipping the model engine.
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return personalToolFailure("tool_access_denied"), nil
	}
	schema, err := compileConversationSchema(in.Definition.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	if in.Call.Name != in.Definition.Key {
		return personalToolFailure("tool_access_denied"), nil
	}
	if strings.HasPrefix(in.Call.Name, "artifact_") {
		return h.invokeArtifactTool(ctx, in)
	}
	switch in.Call.Name {
	case "memory_save", "memory_forget", "todo_create", "todo_update", "todo_delete":
		return h.repo.(persistence.ConversationPersonalMutationRepository).ApplyPersonalTool(ctx, in)
	case "todo_get":
		var args struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		item, err := h.repo.(persistence.ConversationTodoRepository).Todo(ctx, args.ID, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return personalToolResult(item)
	case "todo_list":
		var args struct {
			Query   string `json:"query"`
			Status  string `json:"status"`
			Scope   string `json:"scope"`
			BatchID string `json:"batch_id"`
			Cursor  string `json:"cursor"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		query := agentsdk.ConversationTodoQuery{Query: args.Query, Status: args.Status, BatchID: args.BatchID, Cursor: args.Cursor, Limit: args.Limit}
		if args.Scope == "current_conversation" {
			if in.ConversationID == "" {
				return personalToolFailure("todo_conversation_required"), nil
			}
			query.SourceConversationID = in.ConversationID
		}
		page, err := h.repo.(persistence.ConversationTodoRepository).Todos(ctx, query, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if len(page.Items) == 0 && (args.Query != "" || args.Scope == "current_conversation" || args.BatchID != "") {
			scope := "all"
			if args.Scope == "current_conversation" {
				scope = args.Scope
			}
			return personalToolResult(struct {
				agentsdk.ConversationTodoPage
				Lookup map[string]any `json:"lookup"`
			}{page, map[string]any{"query": args.Query, "scope": scope, "batch_id": args.BatchID, "status": args.Status, "match_fields": []string{"title", "description"}, "match_mode": "literal_substring", "note": "These results apply only to the supplied filters. Project and conversation names are not searched. A filtered empty page does not mean no todos exist; original titles or batch IDs may be found in available history, or by listing without the query. A current_conversation scope excludes work created in other conversations."}})
		}
		return personalToolResult(page)
	case "time_now":
		var args timeutil.Input
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		out, err := timeutil.Resolve(args, h.now(), h.timezone, auth.UserTimezone)
		if err != nil {
			return personalToolFailure("timezone_invalid"), nil
		}
		return personalToolResult(out)
	case "calculate":
		var args calculationInput
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		value, err := calculateConversation(args)
		if err != nil {
			return personalToolFailure("calculation_invalid"), nil
		}
		return personalToolResult(value)
	case "history_search":
		var args agentsdk.ConversationHistorySearch
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		value, err := h.history.SearchHistory(ctx, args, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if _, ok := h.repo.(persistence.ConversationSourceRepository); ok {
			service := h.sourceService()
			audit := service.sourceAudit(in.Authority, in.ConversationID)
			items := make([]agentsdk.ConversationHistoryHit, 0, len(value.Items))
			for _, hit := range value.Items {
				if hit.Role == "assistant" {
					if _, err := audit.historyReference(ctx, hit.ConversationID, hit.MessageID, hit.RunID); err != nil {
						value.Omitted = true
						continue
					}
				}
				items = append(items, hit)
			}
			value.Items = items
		}
		return personalToolResult(value)
	case "history_read":
		var args struct {
			ConversationID string `json:"conversation_id"`
			MessageID      string `json:"message_id"`
			Offset         int    `json:"offset"`
			MaxBytes       int    `json:"max_bytes"`
		}
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		message, err := h.history.HistoryMessage(ctx, args.ConversationID, args.MessageID, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if _, ok := h.repo.(persistence.ConversationSourceRepository); ok && message.Role == "assistant" {
			service := h.sourceService()
			if _, err := service.sourceAudit(in.Authority, in.ConversationID).historyReference(ctx, message.ConversationID, message.ID, message.RunID); err != nil {
				return personalToolFailure(sourceAccessCode(err)), nil
			}
		}
		if args.MaxBytes == 0 {
			args.MaxBytes = 4096
		}
		if args.Offset > len(message.Content) || args.Offset < len(message.Content) && !utf8.RuneStart(message.Content[args.Offset]) {
			return personalToolFailure("history_offset_invalid"), nil
		}
		end := args.Offset + args.MaxBytes
		if end > len(message.Content) {
			end = len(message.Content)
		}
		for end < len(message.Content) && !utf8.RuneStart(message.Content[end]) {
			end--
		}
		return personalToolResult(map[string]any{"conversation_id": message.ConversationID, "message_id": message.ID, "run_id": message.RunID, "seq": message.Seq, "role": message.Role, "content": message.Content[args.Offset:end], "offset": args.Offset, "next_offset": end, "complete": end == len(message.Content), "created_at": message.CreatedAt})
	case "memory_search":
		var args struct {
			Query           string `json:"query"`
			IncludeDisabled bool   `json:"include_disabled"`
			Cursor          string `json:"cursor"`
		}
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		items, err := h.repo.Memories(ctx, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		type memoryCursor struct {
			Scope, Snapshot, Query string
			Offset                 int
		}
		cursor := memoryCursor{Scope: conversationDigest([]string{in.Authority.RuntimeID, in.Authority.WorkspaceID, in.Authority.UserID}), Snapshot: conversationDigest(items), Query: conversationDigest([]any{args.Query, args.IncludeDisabled})}
		if args.Cursor != "" {
			raw, decodeErr := base64.RawURLEncoding.DecodeString(args.Cursor)
			var previous memoryCursor
			if decodeErr != nil || json.Unmarshal(raw, &previous) != nil || previous.Scope != cursor.Scope || previous.Snapshot != cursor.Snapshot || previous.Query != cursor.Query || previous.Offset < 0 || previous.Offset > len(items) {
				return personalToolFailure("memory_cursor_invalid"), nil
			}
			cursor.Offset = previous.Offset
		}
		found := []agentsdk.ConversationMemory{}
		complete := true
		for index := cursor.Offset; index < len(items); index++ {
			item := items[index]
			if !item.Enabled && !args.IncludeDisabled || !strings.Contains(strings.ToLower(item.Title+"\n"+item.Content), strings.ToLower(args.Query)) {
				continue
			}
			if len(conversationJSONText(found))+len(conversationJSONText(item)) > in.Definition.MaxOutputBytes-1024 {
				complete = false
				cursor.Offset = index
				break
			}
			found = append(found, item)
		}
		next := ""
		if !complete {
			raw, _ := json.Marshal(cursor)
			next = base64.RawURLEncoding.EncodeToString(raw)
		}
		return personalToolResult(map[string]any{"items": found, "complete": complete, "next_cursor": next})
	}
	return personalToolFailure("tool_unavailable"), nil
}

func (h *PersonalConversationHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return h.InvokeConversationTool(ctx, in)
}

func (h *PersonalConversationHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, interaction agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	if _, err := h.repo.Get(ctx, interaction.ConversationID, a); err != nil {
		return agentsdk.ConversationToolAuthorization{}, err
	}
	if policy, ok := h.authorizer.(agentsdk.ConversationInteractionAuthorizer); ok {
		return policy.AuthorizeConversationInteraction(ctx, a, interaction)
	}
	return agentsdk.ConversationToolAuthorization{}, nil
}

func (h *PersonalConversationHost) supportsConversationInteractions() bool {
	_, persistenceReady := h.repo.(persistence.ConversationInteractionRepository)
	_, policyReady := h.authorizer.(agentsdk.ConversationInteractionAuthorizer)
	return persistenceReady && policyReady
}
