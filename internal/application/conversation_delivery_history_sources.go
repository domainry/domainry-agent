package application

import (
	"context"
	"time"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func historyResultReadDefinition(key string) (sdk.ConversationToolDefinition, bool) {
	switch key {
	case "history_search", "history_read", "execution_read", "tool_result_read":
		for _, definition := range sdk.PersonalConversationTools() {
			if definition.Key == key {
				return definition, true
			}
		}
	}
	return sdk.ConversationToolDefinition{}, false
}

func (audit *conversationSourceAudit) deliveryHistoryToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, []sdk.ConversationRunReference, error) {
	definition, known := historyResultReadDefinition(record.Call.Name)
	if !known {
		return false, nil, nil
	}
	handled, err := audit.attestDeliveryPersonalRecord(ctx, owner, record, definition)
	if !handled || err != nil {
		return handled, nil, err
	}
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	var roots []sdk.ConversationRunReference
	if definition.Key == "history_search" || definition.Key == "history_read" {
		roots, err = audit.readDeliveredHistory(ctx, owner, record)
	} else {
		roots, err = audit.readDeliveredExecution(ctx, owner, record)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return true, nil, err
	}
	// Retain the wrapper's original ledger and current read policy when its
	// output later becomes a source, including empty historical pages.
	roots = mergeConversationSources(roots, []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}})
	return true, roots, nil
}

func (audit *conversationSourceAudit) authorizeDeliveredExecutionData(ctx context.Context, id string) error {
	if _, err := audit.s.repo.Get(ctx, id, audit.a); err != nil {
		return err
	}
	// This is raw historical data, even when it refers to the same delegation.
	// The provenance-only delivery exception does not authorize its content.
	return audit.s.authorizeCollaborationConversation(ctx, id, "execution_read", audit.a)
}

type deliveredHistorySlice struct {
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	RunID          string    `json:"run_id"`
	Seq            int64     `json:"seq"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	Offset         int       `json:"offset"`
	NextOffset     int       `json:"next_offset"`
	Complete       bool      `json:"complete"`
	CreatedAt      time.Time `json:"created_at"`
}

func (audit *conversationSourceAudit) readDeliveredHistory(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	ctx, audit = audit.rawExecutionSourceAudit(ctx)
	repo, ok := audit.s.repo.(persistence.ConversationHistoryRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "source_read_unavailable")
	}
	var roots []sdk.ConversationRunReference
	message := func(conversation, id string) (sdk.ConversationMessage, error) {
		if err := audit.authorizeDeliveredExecutionData(ctx, conversation); err != nil {
			return sdk.ConversationMessage{}, err
		}
		m, err := repo.HistoryMessage(ctx, conversation, id, audit.a)
		if err != nil {
			return m, err
		}
		if m.ID != id || m.ConversationID != conversation || (m.Role != "user" && m.Role != "assistant") {
			return m, invalidPersonalReceipt()
		}
		if m.Role == "assistant" {
			if m.RunID == "" || (m.ConversationID == owner.ConversationID && m.RunID == owner.RunID) {
				return m, invalidPersonalReceipt()
			}
			part, err := audit.run(ctx, sdk.ConversationRunReference{ConversationID: m.ConversationID, RunID: m.RunID})
			if err != nil {
				return m, err
			}
			roots = mergeConversationSources(roots, part)
		}
		return m, nil
	}
	if record.Call.Name == "history_search" {
		var args sdk.ConversationHistorySearch
		var page sdk.ConversationHistorySearchResult
		if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &page) != nil || len(page.Items) > 20 || args.Limit > 0 && len(page.Items) > args.Limit {
			return nil, invalidPersonalReceipt()
		}
		if args.ConversationID != "" {
			if err := audit.authorizeDeliveredExecutionData(ctx, args.ConversationID); err != nil {
				return nil, err
			}
		}
		seen := map[string]bool{}
		for _, hit := range page.Items {
			key := conversationDigest([]string{hit.ConversationID, hit.MessageID})
			if seen[key] || args.ConversationID != "" && args.ConversationID != hit.ConversationID {
				return nil, invalidPersonalReceipt()
			}
			seen[key] = true
			m, err := message(hit.ConversationID, hit.MessageID)
			if err != nil {
				return nil, err
			}
			if hit.Seq != m.Seq || hit.Role != m.Role || hit.RunID != "" && hit.RunID != m.RunID || !hit.CreatedAt.Equal(m.CreatedAt) || hit.Excerpt != truncateUTF8(m.Content, 256) {
				return nil, conversationFailure("conflict", "source_snapshot_changed")
			}
		}
		// Newer messages and pagination changes do not replace the saved page.
		return roots, nil
	}
	var args struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		Offset         int    `json:"offset"`
		MaxBytes       int    `json:"max_bytes"`
	}
	var saved deliveredHistorySlice
	if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.ConversationID != args.ConversationID || saved.MessageID != args.MessageID || saved.Offset != args.Offset {
		return nil, invalidPersonalReceipt()
	}
	m, err := message(args.ConversationID, args.MessageID)
	if err != nil {
		return nil, err
	}
	if args.MaxBytes == 0 {
		args.MaxBytes = 4096
	}
	if args.Offset < 0 || args.Offset > len(m.Content) || args.Offset < len(m.Content) && !utf8.RuneStart(m.Content[args.Offset]) {
		return nil, invalidPersonalReceipt()
	}
	end := min(len(m.Content), args.Offset+args.MaxBytes)
	for end < len(m.Content) && !utf8.RuneStart(m.Content[end]) {
		end--
	}
	if saved.RunID != m.RunID || saved.Seq != m.Seq || saved.Role != m.Role || !saved.CreatedAt.Equal(m.CreatedAt) || saved.Content != m.Content[args.Offset:end] || saved.NextOffset != end || saved.Complete != (end == len(m.Content)) {
		return nil, conversationFailure("conflict", "source_snapshot_changed")
	}
	return roots, nil
}
