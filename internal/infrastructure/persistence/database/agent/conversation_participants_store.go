package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func delegationParticipant(d sdk.ConversationDelegation, user, operation string) (sdk.ConversationDelegationParticipant, bool) {
	for _, grant := range d.Participants {
		if grant.UserID == user && grant.Revision > 0 && slices.Contains(grant.Operations, operation) {
			return grant, true
		}
	}
	return sdk.ConversationDelegationParticipant{}, false
}

// Resolve the physical record owner only after an explicit indexed grant.
// The resulting authority is confined to repository reads/message storage;
// it must never replace the actor passed to application/Identity/tool policy.
func (s *ConversationStore) participantDelegation(ctx context.Context, db conversationDB, id string, a sdk.ConversationAuthority, operation string) (sdk.ConversationDelegation, sdk.ConversationAuthority, error) {
	d, err := s.conversationDelegation(ctx, db, id, a)
	var coded *sdk.Error
	if err == nil || !errors.As(err, &coded) || coded.Code != "agent.conversation.delegation_not_found" {
		return d, delegationRecordAuthority(d, a), err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationParticipantTable).Columns("owner_key", "owner_user_id").Where(query.And(query.Equal("viewer_key", conversationOwner(a)), query.Equal("delegation_id", id))).Build()
	if err != nil {
		return d, a, err
	}
	var ownerKey, user string
	if err = db.QueryRowContext(ctx, q, args...).Scan(&ownerKey, &user); errors.Is(err, sql.ErrNoRows) {
		return d, a, conversationError("not_found", "delegation_not_found")
	} else if err != nil {
		return d, a, err
	}
	record := sdk.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: user}
	if conversationOwner(record) != ownerKey {
		return d, a, conversationError("not_found", "delegation_not_found")
	}
	d, err = s.conversationDelegation(ctx, db, id, record)
	if err != nil {
		return d, a, err
	}
	if _, allowed := delegationParticipant(d, a.UserID, operation); !allowed {
		return sdk.ConversationDelegation{}, a, conversationError("not_found", "delegation_not_found")
	}
	return d, record, nil
}

func (s *ConversationStore) participantDelegations(ctx context.Context, a sdk.ConversationAuthority) ([]sdk.ConversationDelegation, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationParticipantTable).Columns("delegation_id").Where(query.Equal("viewer_key", conversationOwner(a))).OrderBy(query.Descending("created_at"), query.Descending("delegation_id")).Limit(101).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []sdk.ConversationDelegation{}
	for _, id := range ids {
		d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "view")
		var coded *sdk.Error
		if errors.As(err, &coded) && coded.Class == "not_found" {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func validParticipantInputs(in []sdk.ConversationDelegationParticipantInput, owner string) bool {
	users := []string{}
	for _, input := range in {
		users = append(users, input.UserID)
		if len(input.Operations) == 0 || len(input.Operations) > len(sdk.ConversationParticipantOperations()) || !slices.Contains(input.Operations, "view") {
			return false
		}
		seen := map[string]bool{}
		for _, op := range input.Operations {
			if !slices.Contains(sdk.ConversationParticipantOperations(), op) || seen[op] {
				return false
			}
			seen[op] = true
		}
	}
	return validateAgentSharingUsers(users, owner) == nil
}

func (s *ConversationStore) SetConversationDelegationParticipants(ctx context.Context, id string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	var out sdk.ConversationDelegation
	if in.Action != "set_participants" || in.Participants == nil || !validParticipantInputs(*in.Participants, a.UserID) || !personalMemoryKey(in.ClientID) || in.ExpectedRevision < 1 {
		return out, conversationError("bad_request", "delegation_participants_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.validatePeerMutation(ctx, tx, in.ToolRequest, a); err != nil {
			return err
		}
		key := conversationHash([]string{"delegation-update", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		var err error
		out, err = s.ownedConversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if out.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		out.ParticipantsRevision++
		grants := []sdk.ConversationDelegationParticipant{}
		for _, input := range *in.Participants {
			operations := append([]string{}, input.Operations...)
			sort.Strings(operations)
			grant := sdk.ConversationDelegationParticipant{UserID: input.UserID, Operations: operations, Revision: out.ParticipantsRevision}
			if old, ok := delegationParticipant(out, input.UserID, "view"); ok && slices.Equal(old.Operations, operations) {
				grant.Revision = old.Revision
			}
			grants = append(grants, grant)
		}
		sort.Slice(grants, func(i, j int) bool { return grants[i].UserID < grants[j].UserID })
		out.Participants = grants
		out.Revision++
		out.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		q, args, err := query.NewDeleteBuilder(s.store.Renderer(), conversationParticipantTable).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", id))).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		for _, grant := range grants {
			viewer := sdk.ConversationAuthority{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: grant.UserID}
			q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationParticipantTable).Columns("owner_key", "delegation_id", "viewer_key", "owner_user_id", "revision", "created_at").Values(conversationOwner(a), id, conversationOwner(viewer), a.UserID, grant.Revision, out.CreatedAt.UnixMilli()).Build()
			if err = conversationExec(ctx, tx, q, args, err); err != nil {
				return err
			}
		}
		if err := s.saveConversationDelegation(ctx, tx, out, in.ExpectedRevision, a); err != nil {
			return err
		}
		if err := s.supersedePeerMessages(ctx, tx, out, a); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

var _ persistence.ConversationDelegationParticipantRepository = (*ConversationStore)(nil)

func (s *ConversationStore) lockConversationParticipantGrant(ctx context.Context, tx *sql.Tx, m sdk.ConversationAgentMessage, a sdk.ConversationAuthority) error {
	viewer := sdk.ConversationAuthority{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: m.ParticipantUserID}
	d, err := s.conversationDelegation(ctx, tx, m.DelegationID, a)
	if err != nil {
		return err
	}
	builder := query.NewSelectBuilder(s.store.Renderer(), conversationParticipantTable).Columns("revision").Where(query.And(query.Equal("owner_key", conversationOwner(delegationRecordAuthority(d, a))), query.Equal("delegation_id", m.DelegationID), query.Equal("viewer_key", conversationOwner(viewer)), query.Equal("revision", m.ParticipantRevision)))
	if profile := s.store.Profile(); profile != nil && profile.Capabilities().RowLock {
		var err error
		builder, err = profile.ApplyClaimLock(builder, false)
		if err != nil {
			return err
		}
	}
	q, args, err := builder.Build()
	if err != nil {
		return err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, q, args...).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationError("conflict", "revision_conflict")
	}
	return err
}

// The receiving owner can discard a still-pending participant message after
// the host has rejected its current sender policy. Already consumed input and
// original text remain immutable audit evidence.
func (s *ConversationStore) SupersedeConversationParticipantMessage(ctx context.Context, id string, a sdk.ConversationAuthority) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		p := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("message_id", id), query.Equal("consumed_run_id", ""))
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(p).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		var m sdk.ConversationAgentMessage
		if err = json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.ParticipantUserID == "" && m.SenderUserID == "" {
			return conversationError("bad_request", "delegation_participants_invalid")
		}
		m.Superseded = true
		reason := "participant_access_revoked"
		if m.ParticipantUserID == "" {
			reason = "sender_access_revoked"
		}
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("payload_json", conversationJSON(m)).Set("consumed_run_id", reason).Where(p).Build()
		return conversationExec(ctx, tx, q, args, err)
	})
}
