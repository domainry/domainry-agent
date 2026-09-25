package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) PublishedConversationSkills(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationSkillVersion, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Columns("version", "candidate_id", "revision", "payload_json", "updated_at").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", "skill"))).OrderBy(query.Ascending("target_key")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentsdk.ConversationSkillVersion{}
	for rows.Next() {
		var version, candidateID string
		var revision, updated int64
		var raw []byte
		if err := rows.Scan(&version, &candidateID, &revision, &raw, &updated); err != nil {
			return nil, err
		}
		var definition agentsdk.SkillSchema
		if unmarshalDurableJSON(raw, &definition) != nil || definition.Version != version {
			return nil, conversationError("unavailable", "skill_configuration_invalid")
		}
		published := time.UnixMilli(updated).UTC()
		out = append(out, agentsdk.ConversationSkillVersion{Definition: definition, Digest: conversationHash(definition), State: "published", Revision: revision, EvaluationID: "evaluation_" + candidateID, CreatedAt: published, PublishedAt: &published})
	}
	return out, rows.Err()
}

func (s *ConversationStore) ConversationCapabilityConfiguration(ctx context.Context, kind, target string, a agentsdk.ConversationAuthority) (agentsdk.ConversationCapabilityConfiguration, bool, error) {
	var out agentsdk.ConversationCapabilityConfiguration
	if err := conversationAuthority(a); err != nil {
		return out, false, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Columns("version", "candidate_id", "revision", "payload_json", "updated_at").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", kind), query.Equal("target_key", target))).Build()
	if err != nil {
		return out, false, err
	}
	var updated int64
	err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&out.Version, &out.CandidateID, &out.Revision, &out.Payload, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.Kind, out.TargetKey, out.UpdatedAt = kind, target, time.UnixMilli(updated).UTC()
	return out, true, nil
}

func (s *ConversationStore) ConversationSkillVersion(ctx context.Context, key, version string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillVersion, error) {
	var out agentsdk.ConversationSkillVersion
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("payload_json", "status", "revision", "created_at", "updated_at").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", "skill"), query.Equal("target_key", key), query.Equal("version", version))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	var status string
	var revision, created, updated int64
	err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw, &status, &revision, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "skill_not_found")
	}
	if err != nil {
		return out, err
	}
	var candidate agentsdk.ConversationImprovementCandidate
	if unmarshalDurableJSON(raw, &candidate) != nil || unmarshalDurableJSON(candidate.Proposal, &out.Definition) != nil {
		return out, conversationError("unavailable", "skill_configuration_invalid")
	}
	out.Digest, out.State, out.Revision = conversationHash(out.Definition), status, revision
	out.CreatedAt = time.UnixMilli(created).UTC()
	if status == "published" {
		published := time.UnixMilli(updated).UTC()
		out.PublishedAt = &published
	}
	if candidate.Evaluation != nil {
		out.EvaluationID = candidate.Evaluation.ID
	}
	return out, nil
}

func (s *ConversationStore) CreateConversationCapabilityFeedback(ctx context.Context, clientID string, prepared agentsdk.ConversationCapabilityFeedback, request agentsdk.ConversationCapabilityFeedbackCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationCapabilityFeedback, error) {
	var out agentsdk.ConversationCapabilityFeedback
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityFeedbackTable).Columns("request_hash", "payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_id", clientID))).Build()
		if err != nil {
			return err
		}
		var hash string
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&hash, &raw)
		if err == nil {
			if hash != conversationHash(request) {
				return conversationError("conflict", "idempotency_conflict")
			}
			return unmarshalDurableJSON(raw, &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out = prepared
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationCapabilityFeedbackTable).Columns("owner_key", "feedback_id", "client_id", "request_hash", "payload_json", "created_at").Values(conversationOwner(a), out.ID, clientID, conversationHash(request), conversationJSON(out), out.CreatedAt.UnixMilli()).Build()
		return conversationExec(ctx, tx, q, args, err)
	})
	return out, err
}

func (s *ConversationStore) ConversationCapabilityFeedbacks(ctx context.Context, ids []string, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationCapabilityFeedback, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	out := []agentsdk.ConversationCapabilityFeedback{}
	for _, id := range ids {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityFeedbackTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("feedback_id", id))).Build()
		if err != nil {
			return nil, err
		}
		var raw []byte
		if err := s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return nil, conversationError("not_found", "feedback_not_found")
		} else if err != nil {
			return nil, err
		}
		var feedback agentsdk.ConversationCapabilityFeedback
		if err := unmarshalDurableJSON(raw, &feedback); err != nil {
			return nil, err
		}
		out = append(out, feedback)
	}
	return out, nil
}

func (s *ConversationStore) CreateConversationImprovementCandidate(ctx context.Context, clientID string, prepared agentsdk.ConversationImprovementCandidate, request agentsdk.ConversationImprovementCandidateCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("request_hash", "payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_id", clientID))).Build()
		if err != nil {
			return err
		}
		var hash string
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&hash, &raw)
		if err == nil {
			if hash != conversationHash(request) {
				return conversationError("conflict", "idempotency_conflict")
			}
			return unmarshalDurableJSON(raw, &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out = prepared
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("owner_key", "candidate_id", "client_id", "kind", "target_key", "version", "status", "revision", "request_hash", "payload_json", "created_at", "updated_at").Values(conversationOwner(a), out.ID, clientID, out.Kind, out.TargetKey, out.Version, out.Status, out.Revision, conversationHash(request), conversationJSON(out), out.CreatedAt.UnixMilli(), out.UpdatedAt.UnixMilli()).Build()
		return conversationExec(ctx, tx, q, args, err)
	})
	return out, err
}

func (s *ConversationStore) conversationImprovementCandidate(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("candidate_id", id))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "improvement_candidate_not_found")
	}
	if err != nil {
		return out, err
	}
	return out, unmarshalDurableJSON(raw, &out)
}

func (s *ConversationStore) ConversationImprovementCandidate(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationImprovementCandidate{}, err
	}
	return s.conversationImprovementCandidate(ctx, s.store.Database(), id, a)
}

func (s *ConversationStore) ConversationImprovementCandidates(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationImprovementCandidate, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("payload_json").Where(query.Equal("owner_key", conversationOwner(a))).OrderBy(query.Descending("updated_at")).Limit(100).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentsdk.ConversationImprovementCandidate{}
	for rows.Next() {
		var raw []byte
		var item agentsdk.ConversationImprovementCandidate
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := unmarshalDurableJSON(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ConversationStore) updateImprovementCandidate(ctx context.Context, tx *sql.Tx, item agentsdk.ConversationImprovementCandidate, expected int64, a agentsdk.ConversationAuthority) error {
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Set("status", item.Status).Set("revision", item.Revision).Set("payload_json", conversationJSON(item)).Set("updated_at", item.UpdatedAt.UnixMilli()).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("candidate_id", item.ID), query.Equal("revision", expected))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) ensureImprovementBaselineSnapshot(ctx context.Context, tx *sql.Tx, current agentsdk.ConversationImprovementCandidate, now time.Time, a agentsdk.ConversationAuthority) error {
	if current.BaselineVersion == "" || len(current.BaselineProposal) == 0 || !json.Valid(current.BaselineProposal) || current.Evaluation == nil || !current.Evaluation.Passed {
		return conversationError("conflict", "improvement_baseline_snapshot_invalid")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", current.Kind), query.Equal("target_key", current.TargetKey), query.Equal("version", current.BaselineVersion))).Build()
	if err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
	if err == nil {
		var existing agentsdk.ConversationImprovementCandidate
		if unmarshalDurableJSON(raw, &existing) != nil || existing.ID == current.ID || conversationHash(existing.Proposal) != conversationHash(current.BaselineProposal) || existing.Evaluation == nil || !existing.Evaluation.Passed {
			return conversationError("conflict", "improvement_baseline_changed")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	id := "baseline_" + conversationHash([]string{current.Kind, current.TargetKey, current.BaselineVersion, conversationHash(current.BaselineProposal)})[:32]
	snapshot := agentsdk.ConversationImprovementCandidate{
		ID: id, Kind: current.Kind, TargetKey: current.TargetKey, Version: current.BaselineVersion,
		BaselineSnapshot: true, Proposal: append(json.RawMessage(nil), current.BaselineProposal...),
		FeedbackIDs: []string{}, Reason: "Configuration baseline archived before publishing " + current.ID,
		Status: "retired", Evaluation: current.Evaluation, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("owner_key", "candidate_id", "client_id", "kind", "target_key", "version", "status", "revision", "request_hash", "payload_json", "created_at", "updated_at").Values(conversationOwner(a), snapshot.ID, "system_"+snapshot.ID, snapshot.Kind, snapshot.TargetKey, snapshot.Version, snapshot.Status, snapshot.Revision, conversationHash(snapshot), conversationJSON(snapshot), snapshot.CreatedAt.UnixMilli(), snapshot.UpdatedAt.UnixMilli()).Build()
	return conversationExec(ctx, tx, q, args, err)
}

func (s *ConversationStore) EvaluateConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementEvaluationWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		key := conversationHash([]string{"improvement_evaluate", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		current, err := s.conversationImprovementCandidate(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if current.Revision != in.ExpectedRevision || current.Status != "candidate" {
			return conversationError("conflict", "improvement_revision_conflict")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		current.Evaluation = &agentsdk.ConversationImprovementEvaluation{ID: "evaluation_" + current.ID, SuiteVersion: in.SuiteVersion, ScenarioIDs: append([]string(nil), in.ScenarioIDs...), BaselineCompleted: in.BaselineCompleted, CandidateCompleted: in.CandidateCompleted, BaselineOmissions: in.BaselineOmissions, CandidateOmissions: in.CandidateOmissions, Regressions: append([]string(nil), in.Regressions...), Passed: in.Passed, Notes: in.Notes, CreatedAt: now}
		current.Status, current.Revision, current.UpdatedAt = "evaluated", current.Revision+1, now
		if err := s.updateImprovementCandidate(ctx, tx, current, in.ExpectedRevision, a); err != nil {
			return err
		}
		out = current
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func (s *ConversationStore) PublishConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementPublish, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		key := conversationHash([]string{"improvement_publish", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		current, err := s.conversationImprovementCandidate(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if current.Revision != in.ExpectedRevision || current.Status != "evaluated" || current.Evaluation == nil || !current.Evaluation.Passed || len(current.Evaluation.Regressions) > 0 {
			return conversationError("conflict", "improvement_evaluation_required")
		}
		var existingRevision int64
		var previousCandidateID, previousVersion string
		var previousPayload []byte
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Columns("revision", "candidate_id", "version", "payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", current.Kind), query.Equal("target_key", current.TargetKey))).Build()
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, q, args...).Scan(&existingRevision, &previousCandidateID, &previousVersion, &previousPayload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previousCandidateID != "" && (previousVersion != current.BaselineVersion || conversationHash(json.RawMessage(previousPayload)) != conversationHash(current.BaselineProposal)) {
			return conversationError("conflict", "improvement_baseline_changed")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if err := s.ensureImprovementBaselineSnapshot(ctx, tx, current, now, a); err != nil {
			return err
		}
		if previousCandidateID != "" && previousCandidateID != current.ID {
			previous, previousErr := s.conversationImprovementCandidate(ctx, tx, previousCandidateID, a)
			if previousErr != nil {
				return previousErr
			}
			if previous.Status == "published" {
				previous.Status, previous.Revision, previous.UpdatedAt = "retired", previous.Revision+1, now
				if err := s.updateImprovementCandidate(ctx, tx, previous, previous.Revision-1, a); err != nil {
					return err
				}
			}
		}
		columns := []string{"owner_key", "kind", "target_key", "version", "candidate_id", "revision", "payload_json", "updated_at"}
		insert := query.NewInsertBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Columns(columns...).Values(conversationOwner(a), current.Kind, current.TargetKey, current.Version, current.ID, existingRevision+1, current.Proposal, now.UnixMilli())
		insert, err = s.store.Profile().ApplyUpsert(insert, []string{"owner_key", "kind", "target_key"}, query.AssignExpression("version", query.InsertedValue("version")), query.AssignExpression("candidate_id", query.InsertedValue("candidate_id")), query.AssignExpression("revision", query.InsertedValue("revision")), query.AssignExpression("payload_json", query.InsertedValue("payload_json")), query.AssignExpression("updated_at", query.InsertedValue("updated_at")))
		if err != nil {
			return err
		}
		q, args, err = insert.Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		current.Status, current.Revision, current.UpdatedAt = "published", current.Revision+1, now
		if err := s.updateImprovementCandidate(ctx, tx, current, in.ExpectedRevision, a); err != nil {
			return err
		}
		out = current
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func (s *ConversationStore) RollbackConversationImprovement(ctx context.Context, id string, in agentsdk.ConversationImprovementRollback, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		key := conversationHash([]string{"improvement_rollback", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		current, err := s.conversationImprovementCandidate(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if current.Revision != in.ExpectedRevision || current.Status != "published" || current.Version == in.TargetVersion {
			return conversationError("conflict", "improvement_revision_conflict")
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationImprovementCandidateTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", current.Kind), query.Equal("target_key", current.TargetKey), query.Equal("version", in.TargetVersion))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return conversationError("not_found", "improvement_version_not_found")
		} else if err != nil {
			return err
		}
		var target agentsdk.ConversationImprovementCandidate
		if unmarshalDurableJSON(raw, &target) != nil || target.Evaluation == nil || !target.Evaluation.Passed {
			return conversationError("conflict", "improvement_evaluation_required")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		var configRevision int64
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Columns("revision").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", current.Kind), query.Equal("target_key", current.TargetKey), query.Equal("candidate_id", current.ID))).Build()
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&configRevision); errors.Is(err, sql.ErrNoRows) {
			return conversationError("conflict", "improvement_revision_conflict")
		} else if err != nil {
			return err
		}
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationCapabilityConfigTable).Set("version", target.Version).Set("candidate_id", target.ID).Set("revision", configRevision+1).Set("payload_json", target.Proposal).Set("updated_at", now.UnixMilli()).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("kind", current.Kind), query.Equal("target_key", current.TargetKey), query.Equal("candidate_id", current.ID), query.Equal("revision", configRevision))).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
		current.Status, current.Revision, current.UpdatedAt = "rolled_back", current.Revision+1, now
		if err := s.updateImprovementCandidate(ctx, tx, current, in.ExpectedRevision, a); err != nil {
			return err
		}
		target.Status, target.Revision, target.UpdatedAt = "published", target.Revision+1, now
		if err := s.updateImprovementCandidate(ctx, tx, target, target.Revision-1, a); err != nil {
			return err
		}
		out = target
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

var _ persistence.ConversationImprovementRepository = (*ConversationStore)(nil)
