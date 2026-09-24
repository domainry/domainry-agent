package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-tools-sdk/timeutil"
)

func personalResultReadDefinition(key string, version ...string) (sdk.ConversationToolDefinition, bool) {
	switch key {
	case "calculate", "time_now", "ask_user", "memory_search", "memory_save", "memory_forget", "todo_list", "todo_get", "todo_create", "todo_update", "todo_delete":
		if len(version) > 0 {
			return sdk.PersonalConversationToolDefinition(key, version[0])
		}
		for _, definition := range sdk.PersonalConversationTools() {
			if definition.Key == key {
				return definition, true
			}
		}
	}
	return sdk.ConversationToolDefinition{}, false
}

func invalidPersonalReceipt() error {
	return conversationFailure("conflict", "source_reference_invalid")
}

func decodePersonalReceipt(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return invalidPersonalReceipt()
	}
	return nil
}

// Personal outputs are owned by Agent/Todo, not opaque external tool results.
// This path exists only inside an authorized delivery source audit and never
// supplies a worker lease or an execution confirmation to a tool host.
func (audit *conversationSourceAudit) deliveryPersonalToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	definition, supported := personalResultReadDefinition(record.Call.Name, record.Definition.Version)
	if scope == "" || !supported {
		return false, nil
	}
	handled, err := audit.attestDeliveryPersonalRecord(ctx, owner, record, definition)
	if !handled || err != nil {
		return handled, err
	}
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	switch definition.Key {
	case "calculate":
		var args calculationInput
		if err = decodePersonalReceipt([]byte(record.Call.Arguments), &args); err == nil {
			var value any
			value, err = calculateConversation(args)
			if err == nil && conversationDigest(value) != conversationDigest(json.RawMessage(record.Result.Content)) {
				err = invalidPersonalReceipt()
			}
		}
	case "time_now":
		err = readPersonalTimeReceipt(record)
	case "ask_user":
		var saved struct {
			Answer        string `json:"answer"`
			InteractionID string `json:"interaction_id"`
		}
		err = decodePersonalReceipt(record.Result.Content, &saved)
		if err == nil && (strings.TrimSpace(saved.Answer) == "" || saved.InteractionID == "") {
			err = invalidPersonalReceipt()
		}
	case "memory_search", "memory_save", "memory_forget":
		err = audit.readPersonalMemoryReceipt(ctx, record)
	default:
		err = audit.readPersonalTodoReceipt(ctx, record)
	}
	if err == nil {
		err = ctx.Err()
	}
	return true, err
}

// Attest the immutable Agent-owned execution; callers provide a known built-in
// definition, then apply its current data-reading policy separately.
func (audit *conversationSourceAudit) attestDeliveryPersonalRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution, definition sdk.ConversationToolDefinition) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	if scope == "" {
		return false, nil
	}
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	reader, ok := audit.s.repo.(persistence.ConversationExecutionReadRepository)
	if !ok {
		return false, nil
	} // Legacy stores retain the original policy.
	if conversationDigest(definition) != conversationDigest(record.Definition) || record.Result == nil || record.State != "completed" || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return true, fmt.Errorf("personal receipt shape: %w", invalidPersonalReceipt())
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return true, err
	}
	if err := audit.connectedTool(ctx, definition.Key); err != nil {
		return true, err
	}
	if _, err := audit.s.repo.Get(ctx, owner.ConversationID, audit.evidenceAuthority(owner)); err != nil {
		return true, err
	}
	// ReadExecutionCall enforces the actual user's conversation/run ownership.
	// Compare the saved call and full result; a model cannot create a receipt by
	// presenting a well-formed value or by naming another user's resource.
	original, err := reader.ReadExecutionCall(ctx, owner.ConversationID, owner.RunID, record.Step, record.Call.ID, audit.evidenceAuthority(owner))
	if err != nil {
		return true, err
	}
	if original.State != "completed" || original.Result == nil || conversationDigest(original.Call) != conversationDigest(record.Call) || conversationDigest(original.Definition) != conversationDigest(record.Definition) || conversationDigest(original.Result) != conversationDigest(record.Result) || original.IdempotencyKey != record.IdempotencyKey {
		return true, fmt.Errorf("personal receipt record: %w", invalidPersonalReceipt())
	}
	schema, err := compileConversationSchema(definition.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(record.Call.Arguments)) != nil {
		return true, fmt.Errorf("personal receipt arguments: %w", invalidPersonalReceipt())
	}
	return true, ctx.Err()
}

func readPersonalTimeReceipt(record persistence.ConversationToolExecution) error {
	var args timeutil.Input
	var saved struct {
		Now            string `json:"now"`
		Timezone       string `json:"timezone"`
		TimezoneSource string `json:"timezone_source"`
		Date           string `json:"date"`
		Weekday        string `json:"weekday"`
		RelativeDate   string `json:"relative_date,omitempty"`
		ResolvedDate   string `json:"resolved_date,omitempty"`
	}
	if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil {
		return invalidPersonalReceipt()
	}
	now, err := time.Parse(time.RFC3339Nano, saved.Now)
	if err != nil {
		return invalidPersonalReceipt()
	}
	zone, err := time.LoadLocation(saved.Timezone)
	if err != nil {
		return invalidPersonalReceipt()
	}
	userZone := ""
	switch saved.TimezoneSource {
	case "requested":
		if args.Timezone == "" {
			return invalidPersonalReceipt()
		}
	case "user_profile":
		userZone = saved.Timezone
	case "host_default":
	default:
		return invalidPersonalReceipt()
	}
	// Reconstruct using the attested original instant and timezone source, not
	// today's clock or a subsequently changed profile timezone.
	value, err := timeutil.Resolve(args, now, zone, userZone)
	if err != nil || conversationDigest(value) != conversationDigest(json.RawMessage(record.Result.Content)) {
		return invalidPersonalReceipt()
	}
	return nil
}

func (audit *conversationSourceAudit) readPersonalMemoryReceipt(ctx context.Context, record persistence.ConversationToolExecution) error {
	if record.Call.Name == "memory_forget" {
		return readPersonalDeletionReceipt(record)
	}
	var memories []sdk.ConversationMemory
	if record.Call.Name == "memory_save" {
		var saved struct {
			Memory sdk.ConversationMemory `json:"memory"`
		}
		if decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.Memory.ID == "" || record.Result.ResourceID != saved.Memory.ID {
			return invalidPersonalReceipt()
		}
		memories = []sdk.ConversationMemory{saved.Memory}
	} else {
		var saved struct {
			Items      []sdk.ConversationMemory `json:"items"`
			Complete   bool                     `json:"complete"`
			NextCursor string                   `json:"next_cursor"`
		}
		if decodePersonalReceipt(record.Result.Content, &saved) != nil {
			return invalidPersonalReceipt()
		}
		memories = saved.Items
	}
	// The current Memory API is authenticated and owner-scoped, with no separate
	// search execution grant. Corrected, disabled or deleted saved values cannot
	// survive as the old memory inside a newly read delivery.
	current, err := audit.s.Memories(ctx, audit.a)
	if err != nil {
		return err
	}
	byID := map[string]sdk.ConversationMemory{}
	for _, item := range current {
		byID[item.ID] = item
	}
	for _, saved := range memories {
		saved = normalizeConversationMemory(saved)
		item, exists := byID[saved.ID]
		if !exists || conversationDigest(item) != conversationDigest(saved) {
			return conversationFailure("forbidden", "source_snapshot_changed")
		}
	}
	return nil
}

func readPersonalDeletionReceipt(record persistence.ConversationToolExecution) error {
	var args struct {
		ID               string `json:"id"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	var saved struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || args.ID == "" || args.ExpectedRevision < 1 || !saved.Deleted || saved.ID != args.ID || record.Result.ResourceID != saved.ID {
		return invalidPersonalReceipt()
	}
	// This immutable acknowledgement contains an ID, not the forgotten content.
	return nil
}
