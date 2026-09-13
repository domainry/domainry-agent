package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) verifyDelegationDelivery(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, delivery sdk.ConversationDelegationDelivery, review *sdk.ConversationDeliveryReview, actor *sdk.ConversationToolRequest, a sdk.ConversationAuthority) (sdk.ConversationDeliveryVerification, error) {
	out := sdk.ConversationDeliveryVerification{DeliveryDigest: conversationHash(delivery), BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, ActorID: a.UserID, Blockers: []string{}, CheckedAt: time.Now().UTC()}
	if review != nil && review.DeliveryDigest != out.DeliveryDigest {
		return out, conversationError("conflict", "delivery_changed")
	}
	if len(d.OutputSchema) > 0 {
		schema, err := execution.CompileSchema(d.OutputSchema)
		if err != nil || execution.ValidateJSON(schema, delivery.Data) != nil {
			return out, conversationError("bad_request", "delivery_schema_invalid")
		}
	}
	method := "user"
	if actor != nil {
		method = "agent"
		c, err := s.get(ctx, db, actor.ConversationID, a)
		if err != nil {
			return out, err
		}
		out.AgentID = c.AgentID
		if out.AgentID == "" {
			out.AgentID = "default"
		}
		out.Source = &sdk.ConversationRunReference{ConversationID: actor.ConversationID, RunID: actor.RunID, BeforeStep: actor.Step + 1}
	}
	lookup := func(ref sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) {
		var record persistence.ConversationToolExecution
		claim := persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}
		found, err := s.readExecutionTool(ctx, db, claim, ref.Step, ref.CallID, &record)
		if err != nil {
			return record, err
		}
		if !found || record.State != "completed" || record.Result == nil || conversationHash(record.Result) != ref.SHA256 {
			return record, conversationError("conflict", "completion_evidence_changed")
		}
		return record, nil
	}
	var err error
	out.Checks, out.Ready, err = execution.EvaluateCompletion(d.Brief, delivery, review, method, lookup)
	if err != nil {
		var coded *sdk.Error
		if errors.As(err, &coded) {
			return out, err
		}
		return out, conversationError("bad_request", "completion_assessment_invalid")
	}
	if len(delivery.Unresolved) > 0 {
		out.Blockers = append(out.Blockers, "unresolved_items")
	}
	for _, check := range out.Checks {
		if check.Verdict != "met" || check.Method == "recipient" || check.Method == "pending" {
			out.Blockers = append(out.Blockers, "completion_conditions_pending")
			break
		}
	}
	if delivery.BriefVersion != d.Brief.Version || max(1, delivery.AgreementRevision) != d.AgreementRevision || len(d.PendingChanges) > 0 {
		out.Ready = false
		out.Blockers = append(out.Blockers, "delivery_outdated")
	}
	for _, issue := range d.Disagreements {
		if execution.DisagreementBlocks(issue, d, out.DeliveryDigest) {
			out.Ready = false
			out.Blockers = append(out.Blockers, "disagreements_pending")
			break
		}
	}
	return out, nil
}

func (s *ConversationStore) PreviewConversationDeliveryVerification(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationDeliveryVerification, error) {
	var out sdk.ConversationDeliveryVerification
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		d, err := s.conversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if d.Delivery == nil {
			return conversationError("conflict", "delegation_delivery_invalid")
		}
		if d.Delivery.BriefVersion != d.Brief.Version || max(1, d.Delivery.AgreementRevision) != d.AgreementRevision {
			out = sdk.ConversationDeliveryVerification{DeliveryDigest: conversationHash(d.Delivery), BriefVersion: d.Delivery.BriefVersion, AgreementRevision: max(1, d.Delivery.AgreementRevision), Checks: []sdk.ConversationCompletionCheck{}, Blockers: []string{"delivery_outdated"}}
			return nil
		}
		out, err = s.verifyDelegationDelivery(ctx, tx, d, *d.Delivery, nil, nil, a)
		return err
	})
	return out, err
}

func (s *ConversationStore) preserveLegacyDelivery(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	if d.Delivery == nil || d.Verification != nil {
		return nil
	}
	d.Verification = &sdk.ConversationDeliveryVerification{DeliveryDigest: conversationHash(d.Delivery), BriefVersion: d.Delivery.BriefVersion, AgreementRevision: max(1, d.Delivery.AgreementRevision), Checks: []sdk.ConversationCompletionCheck{}, Blockers: []string{"legacy_delivery_unverified"}}
	return s.saveDeliveryRecord(ctx, tx, d, "legacy", "Delivery recorded before per-condition verification", a)
}

func (s *ConversationStore) saveDeliveryRecord(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, kind, reason string, a sdk.ConversationAuthority) error {
	if d.Delivery == nil || d.Verification == nil {
		return conversationError("conflict", "completion_verification_missing")
	}
	entry := sdk.ConversationDeliveryRecord{Revision: d.Revision, Kind: kind, Delivery: *d.Delivery, Verification: *d.Verification, Reason: reason}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationDeliveryRecordTable).Columns("owner_key", "delegation_id", "revision", "payload_json").Values(conversationOwner(a), d.ID, d.Revision, conversationJSON(entry)).OnConflictDoNothing("owner_key", "delegation_id", "revision").Build()
	if err = conversationExec(ctx, tx, q, args, err); err != nil {
		return err
	}
	q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationDeliveryRecordTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("revision", d.Revision))).Build()
	if err != nil {
		return err
	}
	var raw []byte
	var saved sdk.ConversationDeliveryRecord
	if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	if conversationHash(saved) != conversationHash(entry) {
		return conversationError("conflict", "delivery_record_conflict")
	}
	return nil
}

func (s *ConversationStore) ConversationDeliveryHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	out := sdk.ConversationDeliveryHistory{Items: []sdk.ConversationDeliveryRecord{}, Complete: true}
	if _, err := s.ConversationDelegation(ctx, id, a); err != nil {
		return out, err
	}
	if before < 0 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	predicate := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", id))
	if before > 0 {
		predicate = query.And(predicate, query.LessThan("revision", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDeliveryRecordTable).Columns("payload_json").Where(predicate).OrderBy(query.Descending("revision")).Limit(21).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var entry sdk.ConversationDeliveryRecord
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return out, err
		}
		out.Items = append(out.Items, entry)
	}
	if len(out.Items) > 20 {
		out.Items = out.Items[:20]
		out.Complete = false
		out.NextBefore = out.Items[len(out.Items)-1].Revision
	}
	return out, rows.Err()
}
