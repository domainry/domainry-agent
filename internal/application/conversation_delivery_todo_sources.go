package application

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (audit *conversationSourceAudit) authorizeTodoReceiptRead(ctx context.Context, id string) error {
	policy := audit.s.options.PersonalAuthorizer
	if policy == nil {
		return conversationFailure("unavailable", "todos_unavailable")
	}
	if err := audit.connectedTool(ctx, "todo_get"); err != nil {
		return err
	}
	definition, _ := personalResultReadDefinition("todo_get")
	args, _ := json.Marshal(map[string]string{"id": id})
	// todo_get is the existing data read permission for the Todo API. Invoke
	// the actual owner authorizer directly, outside the reader Agent's execution
	// profile; no create/update/delete capability is inferred from this grant.
	auth, err := policy.AuthorizeConversationTool(ctx, sdk.ConversationToolRequest{Authority: audit.a, Definition: definition, Call: sdk.ConversationToolCall{Name: definition.Key, Arguments: string(args)}})
	if err != nil {
		return err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	return nil
}

func (audit *conversationSourceAudit) readPersonalTodoReceipt(ctx context.Context, record persistence.ConversationToolExecution) error {
	repo, ok := audit.s.repo.(persistence.ConversationTodoRepository)
	if !ok {
		return conversationFailure("unavailable", "todos_unavailable")
	}
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(record.Call.Arguments), &args) != nil {
		return invalidPersonalReceipt()
	}
	if err := audit.authorizeTodoReceiptRead(ctx, args.ID); err != nil {
		return err
	}
	if record.Call.Name == "todo_delete" {
		return readPersonalDeletionReceipt(record)
	}
	var items []sdk.ConversationTodo
	switch record.Call.Name {
	case "todo_get", "todo_update":
		var item sdk.ConversationTodo
		if decodePersonalReceipt(record.Result.Content, &item) != nil || item.ID == "" || item.ID != args.ID {
			return invalidPersonalReceipt()
		}
		if record.Call.Name == "todo_update" && record.Result.ResourceID != item.ID {
			return invalidPersonalReceipt()
		}
		items = []sdk.ConversationTodo{item}
	case "todo_create":
		var batch sdk.ConversationTodoBatch
		if decodePersonalReceipt(record.Result.Content, &batch) != nil || batch.BatchID == "" || batch.BatchID != record.Result.ResourceID || len(batch.Items) == 0 || len(batch.Items) > 20 {
			return invalidPersonalReceipt()
		}
		for _, item := range batch.Items {
			if item.BatchID != batch.BatchID {
				return invalidPersonalReceipt()
			}
		}
		items = batch.Items
	case "todo_list":
		var page struct {
			sdk.ConversationTodoPage
			Lookup *struct {
				Query       string   `json:"query"`
				Scope       string   `json:"scope"`
				BatchID     string   `json:"batch_id"`
				Status      string   `json:"status"`
				MatchFields []string `json:"match_fields"`
				MatchMode   string   `json:"match_mode"`
				Note        string   `json:"note"`
			} `json:"lookup,omitempty"`
		}
		if decodePersonalReceipt(record.Result.Content, &page) != nil || len(page.Items) > 20 {
			return invalidPersonalReceipt()
		}
		items = page.Items
	default:
		return invalidPersonalReceipt()
	}
	seen := map[string]bool{}
	for _, saved := range items {
		if saved.ID == "" || seen[saved.ID] {
			return invalidPersonalReceipt()
		}
		seen[saved.ID] = true
		if err := audit.authorizeTodoReceiptRead(ctx, saved.ID); err != nil {
			return err
		}
		current, err := repo.Todo(ctx, saved.ID, audit.a)
		if err != nil {
			return err
		}
		// The immutable receipt describes its historical state. The current
		// resource must still exist under the same owner and original identity;
		// later status/revision changes do not rewrite the saved receipt.
		if current.ID != saved.ID || !current.CreatedAt.Equal(saved.CreatedAt) || current.BatchID != saved.BatchID || current.Position != saved.Position || current.SourceConversationID != saved.SourceConversationID || current.SourceRunID != saved.SourceRunID {
			return conversationFailure("forbidden", "source_snapshot_changed")
		}
	}
	return audit.authorizeTodoReceiptRead(ctx, args.ID)
}
