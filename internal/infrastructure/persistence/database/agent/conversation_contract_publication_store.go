package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) contractPublicationRecord(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	var out persistence.ConversationContractPublicationRecord
	if revision < 0 || revision == math.MaxInt64 {
		return out, conversationError("bad_request", "agreement_revision_invalid")
	}
	if revision == 0 {
		revision = max(1, d.AgreementRevision)
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgreementTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(delegationRecordAuthority(d, a))), query.Equal("delegation_id", d.ID), query.Equal("revision", revision))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); err == sql.ErrNoRows {
		return out, conversationError("forbidden", "contract_sources_unverified")
	} else if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Agreement); err != nil {
		return out, err
	}
	if out.Agreement.Revision != revision {
		return out, conversationError("forbidden", "contract_sources_unverified")
	}
	if out.Agreement.Requirements != nil {
		out.Requirements = *out.Agreement.Requirements
		return out, nil
	}
	requirements, found, err := s.originalContractRequirements(ctx, db, d, out.Agreement, a)
	if err != nil {
		return out, err
	}
	if !found {
		return out, conversationError("forbidden", "contract_sources_unverified")
	}
	out.Requirements = requirements
	return out, nil // Do not modify the legacy JSON or its nil snapshot marker.
}

func (s *ConversationStore) originalContractRequirements(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, agreement sdk.ConversationAgreementRevision, a sdk.ConversationAuthority) (sdk.ConversationAgentRequirements, bool, error) {
	var result sdk.ConversationAgentRequirements
	found := false
	merge := func(value sdk.ConversationAgentRequirements) error {
		if found && conversationHash(value) != conversationHash(result) {
			return conversationError("forbidden", "contract_sources_unverified")
		}
		result, found = value, true
		return nil
	}
	subjects, _, err := s.delegationSubjects(ctx, db, d.ID, delegationRecordAuthority(d, a))
	if err != nil {
		return result, false, err
	}
	assignments, err := s.conversationAssignments(ctx, db, d, subjects.execution)
	if err != nil {
		return result, false, err
	}
	if agreement.Revision == 1 {
		// Admission idempotency receipts are immutable complete original
		// responses. Later mutations have a higher relationship revision.
		// Page by the immutable key rather than limiting recovery to the most
		// recent mutations or querying today's task declaration.
		after := ""
		for pages := 0; pages < 256; pages++ {
			p := query.Equal("owner_key", conversationOwner(subjects.source))
			if after != "" {
				p = query.And(p, query.GreaterThan("mutation_id", after))
			}
			q, args, e := query.NewSelectBuilder(s.store.Renderer(), conversationCollaborationMutationTable).Columns("mutation_id", "payload_json").Where(p).OrderBy(query.Ascending("mutation_id")).Limit(64).Build()
			if e != nil {
				return result, false, e
			}
			rows, e := db.QueryContext(ctx, q, args...)
			if e != nil {
				return result, false, e
			}
			count := 0
			for rows.Next() {
				var raw []byte
				if e = rows.Scan(&after, &raw); e != nil {
					break
				}
				count++
				var original sdk.ConversationDelegation
				if json.Unmarshal(raw, &original) != nil {
					continue
				} // Other collaboration response types.
				var fields map[string]json.RawMessage
				if json.Unmarshal(raw, &fields) != nil || fields["requirements"] == nil {
					continue
				}
				if original.ID != d.ID || original.Revision != 1 || original.Status != "accepted" || max(1, original.AgreementRevision) != 1 || original.SourceConversationID != d.SourceConversationID || original.OwnerUserID != d.OwnerUserID || !original.CreatedAt.Equal(d.CreatedAt) || original.ConversationID != "conv_"+conversationHash(d.ID)[:32] || original.TaskID != "task_"+conversationHash(d.ID)[:32] {
					continue
				}
				if len(assignments) == 0 || assignments[0].Number != 1 || assignments[0].TaskID != original.TaskID || assignments[0].ConversationID != original.ConversationID || assignments[0].AgentID != original.ToAgentID {
					e = conversationError("forbidden", "contract_sources_unverified")
					break
				}
				if conversationHash(original.Brief) != conversationHash(agreement.Brief) || conversationHash(original.StructuredInput) != conversationHash(agreement.StructuredInput) || conversationHash(original.Dependencies) != conversationHash(agreement.Dependencies) || conversationHash(original.BriefSource) != conversationHash(agreement.Source) || conversationHash(original.InputSource) != conversationHash(agreement.InputSource) {
					e = conversationError("forbidden", "contract_sources_unverified")
					break
				}
				if e = merge(original.Requirements); e != nil {
					break
				}
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return result, false, e
			}
			if count < 64 {
				break
			}
			if pages == 255 {
				return result, false, conversationError("unavailable", "source_limit_exceeded")
			}
		}
	}
	// The completed original admission is authoritative even before execution
	// starts. Its exact result ID, step and immutable actor must all match.
	if ref := agreement.ChangeSource; agreement.Revision == 1 && agreement.FromAgentID != "" && ref != nil && ref.ConversationID == d.SourceConversationID && ref.BeforeStep >= 1 && ref.BeforeStep <= 256 {
		run, e := s.runRow(ctx, db, ref.ConversationID, ref.RunID, subjects.source)
		if e != nil {
			return result, false, e
		}
		c, e := s.get(ctx, db, ref.ConversationID, subjects.source)
		if e != nil {
			return result, false, e
		}
		agentID := c.AgentID
		if agentID == "" {
			agentID = "default"
		}
		if conversationAuthority(run.Authority) != nil || conversationOwner(run.Authority) != conversationOwner(subjects.source) || run.Run.ID != ref.RunID || run.Run.ConversationID != ref.ConversationID || agentID != agreement.FromAgentID {
			return result, false, conversationError("forbidden", "contract_sources_unverified")
		}
		claim := persistence.ConversationClaim{Authority: run.Authority, Run: run.Run}
		payloads, e := s.publicationPayloads(ctx, db, conversationRunStepTable, query.And(conversationRunStepKindPredicate(conversationRunStepKindTool), executionScope(claim, ref.BeforeStep-1)))
		if e != nil {
			return result, false, e
		}
		for _, raw := range payloads {
			var record persistence.ConversationToolExecution
			if json.Unmarshal(raw, &record) != nil {
				return result, false, conversationError("forbidden", "contract_sources_unverified")
			}
			if record.Step != ref.BeforeStep-1 || record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" || record.Result.ResourceID != d.ID || record.Call.Name != "agent_delegate" || record.Definition.Key != record.Call.Name || record.Definition.Effect != "write" {
				continue
			}
			var request sdk.ConversationDelegationCreate
			if json.Unmarshal([]byte(record.Call.Arguments), &request) != nil {
				return result, false, conversationError("forbidden", "contract_sources_unverified")
			}
			if e = merge(request.Requirements); e != nil {
				return result, false, e
			}
		}
	}
	for _, assignment := range assignments {
		if assignment.AgreementRevision > agreement.Revision {
			continue
		}
		executor, e := s.assignmentExecutionAuthority(ctx, db, d, assignment, subjects.execution)
		if e != nil {
			return result, false, e
		}
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), agentRunTable).Columns(conversationRunColumns...).Where(conversationRunScope(executor, assignment.ConversationID)).Limit(257).Build()
		if e != nil {
			return result, false, e
		}
		rows, e := db.QueryContext(ctx, q, args...)
		if e != nil {
			return result, false, e
		}
		count := 0
		for rows.Next() {
			count++
			if count > 256 {
				e = conversationError("unavailable", "source_limit_exceeded")
				break
			}
			run, scanErr := scanConversationRun(rows)
			if scanErr != nil {
				e = scanErr
				break
			}
			background := run.Run.BackgroundTask
			if !run.HasRequirementsSnapshot || background == nil || len(background.Requirements.Sources) == 0 || background.DelegationID != d.ID || background.TaskID != assignment.TaskID || max(1, background.AgreementRevision) != agreement.Revision {
				continue
			}
			if conversationAuthority(run.Authority) != nil || conversationOwner(run.Authority) != conversationOwner(executor) || run.Run.ConversationID != assignment.ConversationID || background.BriefVersion != agreement.Brief.Version || run.Run.Agent != nil && run.Run.Agent.DelegationRoleKey != "" && run.Run.Agent.DelegationRoleKey != run.Authority.RoleKey {
				e = conversationError("forbidden", "contract_sources_unverified")
				break
			}
			if e = merge(background.Requirements); e != nil {
				break
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return result, false, e
		}
	}
	return result, found, nil
}

func (s *ConversationStore) ConversationContractPublicationRecord(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return persistence.ConversationContractPublicationRecord{}, err
	}
	return s.contractPublicationRecord(ctx, s.store.Database(), d, revision, a)
}

func (s *ConversationStore) republishContract(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if in.ContractPublication == nil || in.Publication != nil || in.ToolRequest != nil || len(in.ContractPublication.RecordDigest) != 64 || in.Delivery != nil || in.Brief != nil || in.Review != nil || in.Disagreement != nil || in.Transfer != nil || in.Inspection != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Participants != nil {
		return d, conversationError("bad_request", "contract_publication_invalid")
	}
	subjects, _, err := s.delegationSubjects(ctx, tx, d.ID, a)
	if err != nil {
		return d, err
	}
	if conversationOwner(a) != conversationOwner(subjects.source) || d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
		return d, conversationError("forbidden", "delegation_actor_invalid")
	}
	if d.Revision != in.ExpectedRevision {
		return d, conversationError("conflict", "revision_conflict")
	}
	original, err := s.contractPublicationRecord(ctx, tx, d, in.ContractPublication.AgreementRevision, a)
	if err != nil {
		return d, err
	}
	if conversationHash(original) != in.ContractPublication.RecordDigest {
		return d, conversationError("conflict", "contract_record_changed")
	}
	for _, reader := range []sdk.ConversationAuthority{a, subjects.execution} {
		if _, err = s.flattenTaskDependencies(ctx, tx, original.Agreement.Dependencies, reader); err != nil {
			return d, err
		}
	}
	// Upstream publications keep their own publisher and current audience.
	// Republishing this agreement does not publish an upstream private source.
	direct := original
	direct.Agreement.Dependencies = nil
	if err = s.saveSourceReleases(ctx, tx, d.ID, "contract", a, subjects.execution, direct.SourceReferences()); err != nil {
		return d, err
	}
	d.Revision++
	d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err = s.saveConversationDelegation(ctx, tx, d, in.ExpectedRevision, a); err != nil {
		return d, err
	}
	// Store the original binding and the actual publication actor independently.
	entry := sdk.ConversationContractPublicationReceipt{ConversationContractPublication: *in.ContractPublication, Revision: d.Revision, Publisher: a, RecipientUserID: subjects.execution.UserID, PublishedAt: d.UpdatedAt, Reason: in.Reason}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationContractPublicationTable).Columns("owner_key", "delegation_id", "revision", "payload_json").Values(conversationOwner(delegationRecordAuthority(d, a)), d.ID, entry.Revision, conversationJSON(entry)).Build()
	return d, conversationExec(ctx, tx, q, args, err)
}

func (s *ConversationStore) ConversationContractPublicationHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationHistory, error) {
	out := sdk.ConversationContractPublicationHistory{Items: []sdk.ConversationContractPublicationReceipt{}, Complete: true}
	if before < 0 || before == math.MaxInt64 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	p := query.And(query.Equal("owner_key", conversationOwner(delegationRecordAuthority(d, a))), query.Equal("delegation_id", d.ID))
	if before > 0 {
		p = query.And(p, query.LessThan("revision", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationContractPublicationTable).Columns("payload_json").Where(p).OrderBy(query.Descending("revision")).Limit(21).Build()
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
		var item sdk.ConversationContractPublicationReceipt
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(out.Items) > 20 {
		out.Complete = false
		out.Items = out.Items[:20]
		out.NextBefore = out.Items[19].Revision
	}
	return out, rows.Err()
}

var _ persistence.ConversationContractPublicationRepository = (*ConversationStore)(nil)
