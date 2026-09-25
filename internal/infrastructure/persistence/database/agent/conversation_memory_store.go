package agent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) Summary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSummary, error) {
	var out agentsdk.ConversationSummary
	c, err := s.Get(ctx, id, a)
	if err != nil || c.SummaryID == "" {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(conversationItemScope(a, id, conversationItemSummary), query.Equal("reference_id", c.SummaryID))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		return out, err
	}
	err = unmarshalDurableJSON(raw, &out)
	return out, err
}
func (s *ConversationStore) SaveSummary(ctx context.Context, claim agentpersistence.ConversationClaim, summary agentsdk.ConversationSummary) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		v := old
		c, err := s.get(ctx, tx, claim.Run.ConversationID, claim.Authority)
		if err != nil {
			return err
		}
		if claim.Run.BackgroundTask == nil && c.ActiveRunID != claim.Run.ID || c.SummaryID != summary.PreviousID || summary.ConversationID != c.ID || summary.ThroughSeq >= claim.Run.UserSeq || summary.ThroughSeq <= 0 {
			return conversationError("conflict", "summary_stale")
		}
		if summary.PreviousID != "" {
			q, args, e := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("seq").Where(query.And(conversationItemScope(claim.Authority, c.ID, conversationItemSummary), query.Equal("reference_id", summary.PreviousID))).Build()
			if e != nil {
				return e
			}
			var through int64
			if e = tx.QueryRowContext(ctx, q, args...).Scan(&through); e != nil {
				return e
			}
			if summary.ThroughSeq <= through && !summary.Rebuild {
				return conversationError("conflict", "summary_stale")
			}
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(conversationItemScope(claim.Authority, c.ID, conversationItemMessage), query.Equal("seq", summary.ThroughSeq))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
			return err
		}
		var boundary agentsdk.ConversationMessage
		if err = unmarshalDurableJSON(raw, &boundary); err != nil {
			return err
		}
		if boundary.Role != "assistant" {
			return conversationError("bad_request", "summary_boundary_invalid")
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationItemTable).Columns("owner_key", "conversation_id", "item_kind", "item_key", "reference_id", "run_id", "seq", "payload_json").Values(conversationOwner(claim.Authority), c.ID, conversationItemSummary, summary.ID, summary.ID, claim.Run.ID, summary.ThroughSeq, conversationJSON(summary)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		c.SummaryID = summary.ID
		if err = s.save(ctx, tx, c, c.Revision, claim.Authority); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "context.compacted", map[string]any{"summary_id": summary.ID, "through_seq": summary.ThroughSeq}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, v, old)
	})
}
func (s *ConversationStore) Memories(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	return s.memories(ctx, s.store.Database(), a)
}
func (s *ConversationStore) memories(ctx context.Context, db conversationDB, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_user_memories").Columns("payload_json").Where(query.Equal("owner_key", conversationOwner(a))).OrderBy(query.Ascending("memory_id")).Limit(33).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentsdk.ConversationMemory{}
	for rows.Next() {
		var raw []byte
		var m agentsdk.ConversationMemory
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = unmarshalDurableJSON(raw, &m); err != nil {
			return nil, err
		}
		out = append(out, normalizeStoredConversationMemory(m))
	}
	return out, rows.Err()
}

func normalizeStoredConversationMemory(memory agentsdk.ConversationMemory) agentsdk.ConversationMemory {
	if memory.Kind == "" {
		memory.Kind = agentsdk.ConversationMemoryKindUserPreference
	}
	if memory.Scope.Kind == "" {
		memory.Scope.Kind = agentsdk.ConversationMemoryScopeWorkspace
	}
	if memory.AppliesTo == nil {
		memory.AppliesTo = []string{}
	}
	return memory
}

func storedMemoryInput(memory agentsdk.ConversationMemory) any {
	source := cloneStoredMemorySource(memory.Source)
	if source != nil {
		source.CapturedAt = time.Time{}
	}
	return struct {
		Kind        string                                 `json:"kind"`
		Title       string                                 `json:"title"`
		Content     string                                 `json:"content"`
		Enabled     bool                                   `json:"enabled"`
		Scope       agentsdk.ConversationMemoryScope       `json:"scope"`
		AppliesTo   []string                               `json:"applies_to"`
		Source      *agentsdk.ConversationMemorySource     `json:"source,omitempty"`
		Correction  *agentsdk.ConversationMemoryCorrection `json:"correction,omitempty"`
		Uncertainty string                                 `json:"uncertainty,omitempty"`
	}{memory.Kind, memory.Title, memory.Content, memory.Enabled, memory.Scope, memory.AppliesTo, source, memory.Correction, memory.Uncertainty}
}

func storedMemoryRequestMatches(memory agentsdk.ConversationMemory, in agentsdk.ConversationMemoryWrite) bool {
	requested := agentsdk.ConversationMemory{
		Kind:        in.Kind,
		Title:       in.Title,
		Content:     in.Content,
		Enabled:     in.Enabled,
		Scope:       in.Scope,
		AppliesTo:   in.AppliesTo,
		Source:      cloneStoredMemorySource(in.Source),
		Uncertainty: in.Uncertainty,
	}
	current := memory
	current.Correction = nil
	if conversationHash(storedMemoryInput(current)) != conversationHash(storedMemoryInput(requested)) {
		return false
	}
	reason := strings.TrimSpace(in.CorrectionReason)
	return reason == "" || memory.Correction != nil && memory.Correction.Reason == reason
}

func (s *ConversationStore) appendMemoryChange(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, operation string, memory agentsdk.ConversationMemory) error {
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationMemoryChangeTable).Columns("owner_key", "memory_id", "revision", "operation", "payload_json", "created_at").Values(conversationOwner(a), memory.ID, memory.Revision, operation, conversationJSON(memory), time.Now().UTC().UnixMilli()).Build()
	return conversationExec(ctx, tx, q, args, err)
}
func (s *ConversationStore) WriteMemory(ctx context.Context, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error) {
	var out agentsdk.ConversationMemory
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		return s.writeMemory(ctx, tx, in, a, &out, false)
	})
	return out, err
}

func (s *ConversationStore) writeMemory(ctx context.Context, tx *sql.Tx, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority, out *agentsdk.ConversationMemory, strictRevision bool) error {
	if in.Kind == "" {
		in.Kind = agentsdk.ConversationMemoryKindUserPreference
	}
	if in.Scope.Kind == "" {
		in.Scope.Kind = agentsdk.ConversationMemoryScopeWorkspace
	}
	in.AppliesTo = normalizeStoredMemoryTopics(in.AppliesTo)
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_user_memories").Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID))).Build()
	if err != nil {
		return err
	}
	var raw []byte
	var old agentsdk.ConversationMemory
	err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists {
		if err = unmarshalDurableJSON(raw, &old); err != nil {
			return err
		}
		old = normalizeStoredConversationMemory(old)
		if old.Revision != in.ExpectedRevision {
			if !strictRevision && storedMemoryRequestMatches(old, in) {
				*out = old
				return nil
			}
			return conversationError("conflict", "revision_conflict")
		}
		if storedMemoryRequestMatches(old, in) {
			*out = old
			return nil
		}
	} else {
		if in.ExpectedRevision != 0 {
			return conversationError("conflict", "revision_conflict")
		}
		items, err := s.memories(ctx, tx, a)
		if err != nil {
			return err
		}
		if len(items) >= 32 {
			return conversationError("conflict", "memory_limit")
		}
		lookup, lookupArgs, lookupErr := query.NewSelectBuilder(s.store.Renderer(), conversationMemoryChangeTable).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID))).Build()
		if lookupErr != nil {
			return lookupErr
		}
		var changes int64
		if lookupErr = tx.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&changes); lookupErr != nil {
			return lookupErr
		}
		if changes != 0 {
			return conversationError("conflict", "memory_id_reused")
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	correction := old.Correction
	if strings.TrimSpace(in.CorrectionReason) != "" {
		correction = &agentsdk.ConversationMemoryCorrection{PreviousRevision: old.Revision, Reason: strings.TrimSpace(in.CorrectionReason)}
	}
	*out = agentsdk.ConversationMemory{ID: in.ID, Kind: in.Kind, Title: in.Title, Content: in.Content, Enabled: in.Enabled, Scope: in.Scope, AppliesTo: in.AppliesTo, Source: cloneStoredMemorySource(in.Source), Correction: correction, Uncertainty: in.Uncertainty, Revision: old.Revision + 1, CreatedAt: old.CreatedAt, UpdatedAt: now}
	if exists && conversationHash(storedMemoryInput(old)) == conversationHash(storedMemoryInput(*out)) {
		*out = old
		return nil
	}
	if !exists {
		out.CreatedAt = now
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_user_memories").Columns("owner_key", "memory_id", "revision", "payload_json").Values(conversationOwner(a), in.ID, out.Revision, conversationJSON(out)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		return s.appendMemoryChange(ctx, tx, a, "create", *out)
	}
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), "_agent_user_memories").Set("payload_json", conversationJSON(out)).Set("revision", out.Revision).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID), query.Equal("revision", in.ExpectedRevision))).Build()
	if err = conversationCAS(ctx, tx, q, args, err); err != nil {
		return err
	}
	return s.appendMemoryChange(ctx, tx, a, "update", *out)
}

func normalizeStoredMemoryTopics(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func cloneStoredMemorySource(source *agentsdk.ConversationMemorySource) *agentsdk.ConversationMemorySource {
	if source == nil {
		return nil
	}
	out := *source
	return &out
}

func (s *ConversationStore) deleteMemory(ctx context.Context, tx *sql.Tx, id string, revision int64, a agentsdk.ConversationAuthority) error {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_user_memories").Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", id), query.Equal("revision", revision))).Build()
	if err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return conversationError("conflict", "revision_conflict")
		}
		return err
	}
	var deleted agentsdk.ConversationMemory
	if err = unmarshalDurableJSON(raw, &deleted); err != nil {
		return err
	}
	deleted = normalizeStoredConversationMemory(deleted)
	deleted.Revision++
	deleted.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err = s.appendMemoryChange(ctx, tx, a, "delete", deleted); err != nil {
		return err
	}
	q, args, err = query.NewDeleteBuilder(s.store.Renderer(), "_agent_user_memories").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", id), query.Equal("revision", revision))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) DeleteMemory(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	if err := conversationAuthority(a); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error { return s.deleteMemory(ctx, tx, id, revision, a) })
}
