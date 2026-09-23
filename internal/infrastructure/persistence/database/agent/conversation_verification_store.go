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
		owner := a
		if d.OwnerUserID != a.UserID && participantDeliveryReading(d, a) {
			var err error
			owner, err = s.delegationExecutionAuthority(ctx, db, d, a)
			if err != nil {
				return record, err
			}
		} else if d.ExecutionSubject != nil && d.ExecutionSubject.UserID != a.UserID {
			subjects, found, err := s.delegationSubjects(ctx, db, d.ID, a)
			if err != nil {
				return record, err
			}
			if found {
				owner = subjects.execution
			}
		}
		claim := persistence.ConversationClaim{Authority: owner, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}
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
		d, _, err := s.participantDelegation(ctx, tx, id, a, "delivery_read")
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

func (s *ConversationStore) saveDeliveryRecord(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, kind, reason string, a sdk.ConversationAuthority) error {
	if d.Delivery == nil || d.Verification == nil {
		return conversationError("conflict", "completion_verification_missing")
	}
	// Review and acceptance record assessments, not a
	// new publication by the recipient. They must not manufacture a grant.
	if kind == "deliver" {
		if err := s.saveDelegationDeliveryReleases(ctx, tx, d, a); err != nil {
			return err
		}
	}
	entry := sdk.ConversationDeliveryRecord{Revision: d.Revision, Kind: kind, Delivery: *d.Delivery, Verification: *d.Verification, Reason: reason}
	return s.insertDelegationHistory(ctx, tx, conversationItemDelegationDelivery, d, "", d.Revision, entry.Verification.Source, entry, a)
}

func (s *ConversationStore) ConversationDeliveryHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	out := sdk.ConversationDeliveryHistory{Items: []sdk.ConversationDeliveryRecord{}, Complete: true}
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	if before < 0 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	predicate := delegationHistoryPredicate(conversationOwner(delegationRecordAuthority(d, a)), conversationItemDelegationDelivery, id, "")
	if before > 0 {
		predicate = query.And(predicate, query.LessThan("seq", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(predicate).OrderBy(query.Descending("seq")).Limit(21).Build()
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
