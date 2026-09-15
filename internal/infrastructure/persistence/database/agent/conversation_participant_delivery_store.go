package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func participantDeliveryReading(d sdk.ConversationDelegation, a sdk.ConversationAuthority) bool {
	grant, found := delegationParticipant(d, a.UserID, "delivery_read")
	return found && sdk.ParticipantPublisherVerified(grant, d.OwnerUserID, a)
}

// The issuer may relay only sources already explicitly published to it in
// this delivery. Keep the original publisher and provider; an issuer grant
// never manufactures another user's publication or execution identity.
func (s *ConversationStore) shareParticipantDeliveryRoots(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, publisher, reader sdk.ConversationAuthority, refs []sdk.ConversationRunReference) error {
	return s.shareParticipantPublishedRoots(ctx, tx, d, "delivery", publisher, reader, refs)
}

func (s *ConversationStore) shareParticipantPublishedRoots(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, purpose string, publisher, reader sdk.ConversationAuthority, refs []sdk.ConversationRunReference) error {
	seen := map[sdk.ConversationRunReference]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, publisher); err == nil && purpose != "execution" {
			if err := s.saveSourceReleases(ctx, tx, d.ID, purpose, publisher, reader, []sdk.ConversationRunReference{ref}); err != nil {
				return err
			}
			continue
		} else if err != nil {
			var coded *sdk.Error
			if !errors.As(err, &coded) || coded.Class != "not_found" {
				return err
			}
		}
		prefix := query.Equal("before_step", 0)
		if ref.BeforeStep > 0 {
			prefix = query.Or(prefix, query.GreaterThanOrEqual("before_step", ref.BeforeStep))
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(publisher)), query.Equal("delegation_id", d.ID), query.Equal("conversation_id", ref.ConversationID), query.Equal("run_id", ref.RunID), prefix)).OrderBy(query.Ascending("release_id")).Limit(33).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		releases := []persistence.ConversationSourceRelease{}
		for rows.Next() {
			var raw []byte
			var release persistence.ConversationSourceRelease
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal(raw, &release)
			}
			if err != nil {
				break
			}
			if release.Purpose == purpose && release.DelegationID == d.ID && release.Publisher != nil && release.Producer.RuntimeID == publisher.RuntimeID && release.Producer.WorkspaceID == publisher.WorkspaceID && release.Publisher.UserID == release.Producer.UserID && conversationAuthority(release.Producer) == nil && conversationAuthority(*release.Publisher) == nil {
				// Narrow the relay to the submitted prefix, even when the issuer
				// holds a wider original publication for provenance traversal.
				release.Reference = ref
				releases = append(releases, release)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(releases) == 0 {
			return conversationError("forbidden", "source_publisher_unverified")
		}
		if len(releases) > 32 {
			return conversationError("unavailable", "source_limit_exceeded")
		}
		for _, release := range releases {
			key := conversationHash(release)
			q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("owner_key", "release_id", "producer_key", "delegation_id", "conversation_id", "run_id", "before_step", "payload_json").Values(conversationOwner(reader), key, conversationOwner(release.Producer), d.ID, ref.ConversationID, ref.RunID, ref.BeforeStep, conversationJSON(release)).OnConflictDoNothing("owner_key", "release_id").Build()
			if err = conversationExec(ctx, tx, q, args, err); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ConversationStore) shareParticipantDeliveryHistory(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, publisher, reader sdk.ConversationAuthority) error {
	refs := []sdk.ConversationRunReference{}
	if d.Delivery != nil {
		record := sdk.ConversationDeliveryRecord{Delivery: *d.Delivery}
		if d.Verification != nil {
			record.Verification = *d.Verification
		}
		refs = append(refs, record.SourceReferences()...)
	}
	// A grant covers submitted history; collect it in the same transaction as
	// the current grant/revision. Arbitrary private execution is never queried.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDeliveryRecordTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(publisher)), query.Equal("delegation_id", d.ID))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		var record sdk.ConversationDeliveryRecord
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &record)
		}
		if err != nil {
			break
		}
		refs = append(refs, record.SourceReferences()...)
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return s.shareParticipantDeliveryRoots(ctx, tx, d, publisher, reader, refs)
}

func (s *ConversationStore) publishDeliveryToParticipants(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, publisher sdk.ConversationAuthority, refs []sdk.ConversationRunReference) error {
	for _, grant := range d.Participants {
		if !sdk.ParticipantPublisherVerified(grant, d.OwnerUserID, publisher) || !slices.Contains(grant.Operations, "delivery_read") {
			continue
		}
		reader := publisher
		reader.UserID, reader.RoleKey = grant.UserID, ""
		if err := s.saveSourceReleases(ctx, tx, d.ID, "delivery", publisher, reader, refs); err != nil {
			return err
		}
	}
	return nil
}
