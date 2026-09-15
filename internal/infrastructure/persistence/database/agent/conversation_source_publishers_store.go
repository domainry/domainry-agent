package agent

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// Legacy rows did not distinguish proof ownership from the publication role.
// Recover only identities attached to an actual matching publication. Neither
// today's task role nor a later review proves who originally shared a source.
// Recovery is a read projection; immutable history and legacy IDs stay intact.
func (s *ConversationStore) restoreLegacySourcePublishers(ctx context.Context, releases []persistence.ConversationSourceRelease, reader sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	out := make([]persistence.ConversationSourceRelease, 0, len(releases))
	for _, release := range releases {
		if release.Publisher != nil {
			out = append(out, release)
			continue
		}
		publishers, err := s.legacySourcePublishers(ctx, release, reader)
		if err != nil {
			return nil, err
		}
		if len(publishers) == 0 {
			out = append(out, release) // Application rejects the unknown publisher.
		}
		for _, publisher := range publishers {
			copy := release
			copy.Publisher = &publisher
			out = append(out, copy)
		}
		if len(out) > 32 {
			return nil, conversationError("unavailable", "source_limit_exceeded")
		}
	}
	if len(out) > 32 {
		return nil, conversationError("unavailable", "source_limit_exceeded")
	}
	return out, nil
}

func (s *ConversationStore) legacyPublicationPayloads(ctx context.Context, table string, scope query.Predicate) ([][]byte, error) {
	return s.publicationPayloads(ctx, s.store.Database(), table, scope)
}

func (s *ConversationStore) publicationPayloads(ctx context.Context, db conversationDB, table string, scope query.Predicate) ([][]byte, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("payload_json").Where(scope).Limit(257).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]byte{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	if len(out) > 256 {
		return nil, conversationError("unavailable", "source_limit_exceeded")
	}
	return out, rows.Err()
}

func (s *ConversationStore) legacySourcePublishers(ctx context.Context, release persistence.ConversationSourceRelease, reader sdk.ConversationAuthority) ([]sdk.ConversationAuthority, error) {
	d, err := s.conversationDelegation(ctx, s.store.Database(), release.DelegationID, reader)
	if err != nil {
		return nil, err
	}
	owner := delegationRecordAuthority(d, reader)
	scope := query.And(query.Equal("owner_key", conversationOwner(owner)), query.Equal("delegation_id", d.ID))
	table := conversationAgreementTable
	if release.Purpose == "delivery" {
		table = conversationDeliveryRecordTable
	} else if release.Purpose == "message" {
		table, scope = conversationAgentMessageTable, delegationMessageScope(d, owner)
	} else if release.Purpose != "contract" {
		return nil, conversationError("unavailable", "source_reference_invalid")
	}
	payloads, err := s.legacyPublicationPayloads(ctx, table, scope)
	if err != nil {
		return nil, err
	}
	publishers := []sdk.ConversationAuthority{}
	seen := map[sdk.ConversationAuthority]bool{}
	for _, raw := range payloads {
		var source *sdk.ConversationRunReference
		var userID, roleKey string
		switch release.Purpose {
		case "contract":
			var entry sdk.ConversationAgreementRevision
			if json.Unmarshal(raw, &entry) != nil {
				return nil, conversationError("unavailable", "source_reference_invalid")
			}
			matches := entry.Source != nil && *entry.Source == release.Reference || entry.InputSource != nil && *entry.InputSource == release.Reference
			if !matches && entry.Revision == 1 && entry.FromAgentID != "" && entry.ChangeSource != nil {
				matches, err = s.legacyRequiredSourcePublication(ctx, *entry.ChangeSource, d.ID, release.Reference, release.Producer)
				if err != nil {
					return nil, err
				}
			}
			if !matches || entry.FromAgentID == "" || entry.ChangeSource == nil {
				continue
			}
			source = entry.ChangeSource
		case "delivery":
			var entry sdk.ConversationDeliveryRecord
			if json.Unmarshal(raw, &entry) != nil {
				return nil, conversationError("unavailable", "source_reference_invalid")
			}
			if entry.Kind != "deliver" || entry.Verification.AgentID == "" || entry.Verification.Source == nil || !deliveryPublishedReference(entry, release.Reference) {
				continue
			}
			source, userID = entry.Verification.Source, entry.Verification.ActorID
		case "message":
			var message sdk.ConversationAgentMessage
			if json.Unmarshal(raw, &message) != nil {
				return nil, conversationError("unavailable", "source_reference_invalid")
			}
			if message.Source == nil || *message.Source != release.Reference || message.FromAgentID == "" {
				continue
			}
			source, userID, roleKey = message.Source, message.SenderUserID, message.SenderRoleKey
		}
		if source == nil || source.ConversationID == "" || source.RunID == "" || source.BeforeStep < 1 || source.BeforeStep > 257 {
			continue
		}
		run, err := s.runRow(ctx, s.store.Database(), source.ConversationID, source.RunID, release.Producer)
		if err != nil {
			return nil, err
		}
		publisher := run.Authority
		if conversationAuthority(publisher) != nil || conversationOwner(publisher) != conversationOwner(release.Producer) || userID != "" && publisher.UserID != userID || roleKey != "" && publisher.RoleKey != roleKey {
			continue
		}
		if release.Purpose == "delivery" && (run.Run.BackgroundTask == nil || run.Run.BackgroundTask.DelegationID != d.ID) || release.Purpose == "contract" && source.ConversationID != d.SourceConversationID {
			continue
		}
		if !seen[publisher] {
			publishers, seen[publisher] = append(publishers, publisher), true
		}
	}
	return publishers, nil
}

// The initial agreement does not duplicate requirements.sources. Its trusted
// change reference locates the original admission call. Only that completed
// agent_delegate result for this exact delegation proves which roots were
// actually submitted; current configuration/task metadata is insufficient.
func (s *ConversationStore) legacyRequiredSourcePublication(ctx context.Context, actor sdk.ConversationRunReference, delegationID string, root sdk.ConversationRunReference, producer sdk.ConversationAuthority) (bool, error) {
	if actor.ConversationID == "" || actor.RunID == "" || actor.BeforeStep < 1 || actor.BeforeStep > 256 {
		return false, nil
	}
	claim := persistence.ConversationClaim{Authority: producer, Run: sdk.ConversationRun{ConversationID: actor.ConversationID, ID: actor.RunID}}
	rows, err := s.legacyPublicationPayloads(ctx, "_agent_conversation_tool_calls", executionScope(claim, actor.BeforeStep-1))
	if err != nil {
		return false, err
	}
	for _, raw := range rows {
		var record persistence.ConversationToolExecution
		if json.Unmarshal(raw, &record) != nil {
			return false, conversationError("unavailable", "source_reference_invalid")
		}
		if record.Step != actor.BeforeStep-1 || record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" || record.Result.ResourceID != delegationID || record.Call.Name != "agent_delegate" || record.Definition.Key != record.Call.Name || record.Definition.Effect != "write" {
			continue
		}
		var request sdk.ConversationDelegationCreate
		if json.Unmarshal([]byte(record.Call.Arguments), &request) != nil {
			return false, conversationError("unavailable", "source_reference_invalid")
		}
		for _, admitted := range request.Requirements.Sources {
			if admitted == root {
				return true, nil
			}
		}
	}
	return false, nil
}

func deliveryPublishedReference(entry sdk.ConversationDeliveryRecord, ref sdk.ConversationRunReference) bool {
	for _, evidence := range entry.Delivery.Evidence {
		if evidence == ref {
			return true
		}
	}
	for _, condition := range entry.Delivery.Conditions {
		for _, receipt := range condition.Receipts {
			if (sdk.ConversationRunReference{ConversationID: receipt.ConversationID, RunID: receipt.RunID, BeforeStep: receipt.Step + 2}) == ref {
				return true
			}
		}
	}
	return entry.Verification.Source != nil && *entry.Verification.Source == ref
}
