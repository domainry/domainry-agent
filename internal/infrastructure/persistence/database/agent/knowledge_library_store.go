package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func libraryScope(a agentsdk.ConversationAuthority) string {
	return conversationHash([]string{a.RuntimeID, a.WorkspaceID})
}
func libraryPredicate(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("scope_key", libraryScope(a)), query.Equal("library_id", id))
}
func validLibraryID(id string) bool {
	return strings.HasPrefix(id, "lib_") && len(id) == 36 && personalMemoryKey(id)
}
func validLibraryUser(id string) bool {
	return executionText(id, 255, true) && strings.TrimSpace(id) == id
}
func validLibraryRole(role string) bool {
	return role == "reader" || role == "editor" || role == "manager"
}

func (s *ConversationStore) library(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var out agentsdk.KnowledgeLibrary
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !validLibraryID(id) {
		return out, conversationError("bad_request", "library_invalid")
	}
	var member agentsdk.KnowledgeLibraryMember
	found, err := s.executionRead(ctx, db, libraryMemberTable, query.And(libraryPredicate(a, id), query.Equal("user_id", a.UserID)), &member)
	if err != nil {
		return out, err
	}
	if !found || !validLibraryRole(member.Role) {
		return out, conversationError("not_found", "library_not_found")
	}
	found, err = s.executionRead(ctx, db, libraryTable, libraryPredicate(a, id), &out)
	if err == nil && (!found || out.Kind == "personal" && out.OwnerUserID != a.UserID) {
		err = conversationError("not_found", "library_not_found")
	}
	out.Role = member.Role
	return out, err
}
func (s *ConversationStore) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error { var e error; out, e = s.library(ctx, tx, id, a); return e })
	return
}

func (s *ConversationStore) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	if err = conversationAuthority(a); err != nil {
		return
	}
	if !personalMemoryKey(in.ClientID) || !executionText(in.Name, 128, true) || !executionText(in.Description, 1024, false) || in.Kind != "personal" && in.Kind != "shared" {
		return out, conversationError("bad_request", "library_invalid")
	}
	id := "lib_" + conversationHash([]string{conversationOwner(a), "shared", in.ClientID})[:32]
	if in.Kind == "personal" {
		id = "lib_" + conversationHash([]string{conversationOwner(a), "personal"})[:32]
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out = agentsdk.KnowledgeLibrary{}
		var prior agentsdk.KnowledgeLibrary
		found, e := s.executionRead(ctx, tx, libraryTable, libraryPredicate(a, id), &prior)
		if e != nil {
			return e
		}
		if found {
			if in.Kind != "personal" {
				q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryTable).Columns("request_hash").Where(libraryPredicate(a, id)).Build()
				if e != nil {
					return e
				}
				var hash string
				if e = tx.QueryRowContext(ctx, q, args...).Scan(&hash); e != nil {
					return e
				}
				if hash != conversationHash(in) {
					return conversationError("conflict", "idempotency_conflict")
				}
			}
			out, e = s.library(ctx, tx, id, a)
			return e
		}
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryTable).Projections(query.Project(query.CountAll())).Where(query.Equal("scope_key", libraryScope(a))).Build()
		if e != nil {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, q, args...).Scan(&count); e != nil {
			return e
		}
		if count >= 1000 {
			return conversationError("conflict", "library_limit")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		out = agentsdk.KnowledgeLibrary{ID: id, Kind: in.Kind, Name: in.Name, Description: in.Description, OwnerUserID: a.UserID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, args, e = query.NewInsertBuilder(s.store.Renderer(), libraryTable).Columns("scope_key", "library_id", "request_hash", "revision", "payload_json").Values(libraryScope(a), id, conversationHash(in), 1, conversationJSON(out)).Build()
		if e = conversationExec(ctx, tx, q, args, e); e != nil {
			return e
		}
		member := agentsdk.KnowledgeLibraryMember{UserID: a.UserID, Role: "manager", UpdatedAt: now}
		q, args, e = query.NewInsertBuilder(s.store.Renderer(), libraryMemberTable).Columns("scope_key", "library_id", "user_id", "role", "payload_json").Values(libraryScope(a), id, a.UserID, member.Role, conversationJSON(member)).Build()
		if e = conversationExec(ctx, tx, q, args, e); e != nil {
			return e
		}
		out.Role = "manager"
		return nil
	})
	return
}

func libraryPageLimit(after string, limit int, users bool) (int, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 || after != "" && ((!users && !validLibraryID(after)) || (users && !validLibraryUser(after))) {
		return 0, conversationError("bad_request", "library_query_invalid")
	}
	return limit, nil
}
func (s *ConversationStore) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryPage, err error) {
	out.Items = []agentsdk.KnowledgeLibrary{}
	out.Complete = true
	if err = conversationAuthority(a); err != nil {
		return
	}
	if limit, err = libraryPageLimit(after, limit, false); err != nil {
		return
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out = agentsdk.KnowledgeLibraryPage{Items: []agentsdk.KnowledgeLibrary{}, Complete: true}
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryMemberTable).Columns("library_id").Where(query.And(query.Equal("scope_key", libraryScope(a)), query.Equal("user_id", a.UserID), query.GreaterThan("library_id", after))).OrderBy(query.Ascending("library_id")).Limit(limit + 1).Build()
		if e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, q, args...)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		_ = rows.Close()
		if e != nil {
			return e
		}
		if len(ids) > limit {
			out.Complete = false
			ids = ids[:limit]
			out.NextAfter = ids[len(ids)-1]
		}
		for _, id := range ids {
			item, e := s.library(ctx, tx, id, a)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, item)
		}
		return nil
	})
	return
}

func (s *ConversationStore) saveLibrary(ctx context.Context, tx *sql.Tx, item *agentsdk.KnowledgeLibrary, expected int64, a agentsdk.ConversationAuthority) error {
	if expected < 1 || item.Revision != expected {
		return conversationError("conflict", "revision_conflict")
	}
	item.Revision++
	item.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	stored := *item
	stored.Role = ""
	q, args, e := query.NewUpdateBuilder(s.store.Renderer(), libraryTable).Set("revision", item.Revision).Set("payload_json", conversationJSON(stored)).Where(query.And(libraryPredicate(a, item.ID), query.Equal("revision", expected))).Build()
	return conversationCAS(ctx, tx, q, args, e)
}
func (s *ConversationStore) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	if !executionText(in.Name, 128, true) || !executionText(in.Description, 1024, false) {
		return out, conversationError("bad_request", "library_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = s.library(ctx, tx, id, a)
		if e != nil {
			return e
		}
		if out.Role != "manager" {
			return conversationError("forbidden", "library_manage_required")
		}
		out.Name, out.Description, out.Archived = in.Name, in.Description, in.Archived
		return s.saveLibrary(ctx, tx, &out, in.ExpectedRevision, a)
	})
	return
}
func (s *ConversationStore) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryMembers, err error) {
	out.Items = []agentsdk.KnowledgeLibraryMember{}
	out.Complete = true
	if limit, err = libraryPageLimit(after, limit, true); err != nil {
		return
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out = agentsdk.KnowledgeLibraryMembers{Items: []agentsdk.KnowledgeLibraryMember{}, Complete: true}
		item, e := s.library(ctx, tx, id, a)
		if e != nil {
			return e
		}
		if item.Role != "manager" {
			return conversationError("forbidden", "library_manage_required")
		}
		out.Revision = item.Revision
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryMemberTable).Columns("payload_json").Where(query.And(libraryPredicate(a, id), query.GreaterThan("user_id", after))).OrderBy(query.Ascending("user_id")).Limit(limit + 1).Build()
		if e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, q, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var member agentsdk.KnowledgeLibraryMember
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &member); e != nil {
				return e
			}
			out.Items = append(out.Items, member)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.Complete = false
			out.NextAfter = out.Items[limit-1].UserID
		}
		return nil
	})
	return
}
func (s *ConversationStore) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	if !validLibraryRole(in.Role) {
		return agentsdk.KnowledgeLibrary{}, conversationError("bad_request", "library_role_invalid")
	}
	return s.mutateLibraryMember(ctx, id, user, in.Role, in.ExpectedRevision, a)
}
func (s *ConversationStore) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.mutateLibraryMember(ctx, id, user, "", expected, a)
}
func (s *ConversationStore) mutateLibraryMember(ctx context.Context, id, user, role string, expected int64, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	if !validLibraryUser(user) {
		return out, conversationError("bad_request", "library_member_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = s.library(ctx, tx, id, a)
		if e != nil {
			return e
		}
		if out.Kind != "shared" {
			return conversationError("forbidden", "personal_library_private")
		}
		if out.Role != "manager" {
			return conversationError("forbidden", "library_manage_required")
		}
		if expected < 1 || out.Revision != expected {
			return conversationError("conflict", "revision_conflict")
		}
		predicate := query.And(libraryPredicate(a, id), query.Equal("user_id", user))
		var prior agentsdk.KnowledgeLibraryMember
		found, e := s.executionRead(ctx, tx, libraryMemberTable, predicate, &prior)
		if e != nil {
			return e
		}
		if !found && role == "" {
			return conversationError("not_found", "library_member_not_found")
		}
		if found && prior.Role == role {
			return nil
		}
		if prior.Role == "manager" && role != "manager" {
			q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryMemberTable).Projections(query.Project(query.CountAll())).Where(query.And(libraryPredicate(a, id), query.Equal("role", "manager"))).Build()
			if e != nil {
				return e
			}
			var count int
			if e = tx.QueryRowContext(ctx, q, args...).Scan(&count); e != nil {
				return e
			}
			if count <= 1 {
				return conversationError("conflict", "library_last_manager")
			}
		}
		if !found {
			q, args, e := query.NewSelectBuilder(s.store.Renderer(), libraryMemberTable).Projections(query.Project(query.CountAll())).Where(libraryPredicate(a, id)).Build()
			if e != nil {
				return e
			}
			var count int
			if e = tx.QueryRowContext(ctx, q, args...).Scan(&count); e != nil {
				return e
			}
			if count >= 1000 {
				return conversationError("conflict", "library_member_limit")
			}
		}
		// Advancing the library revision serializes all settings/member changes,
		// including concurrent attempts to remove the final two managers.
		if e = s.saveLibrary(ctx, tx, &out, expected, a); e != nil {
			return e
		}
		if role == "" {
			q, args, e := query.NewDeleteBuilder(s.store.Renderer(), libraryMemberTable).Where(predicate).Build()
			if e = conversationExec(ctx, tx, q, args, e); e != nil {
				return e
			}
		} else {
			member := agentsdk.KnowledgeLibraryMember{UserID: user, Role: role, UpdatedAt: out.UpdatedAt}
			if found {
				q, args, e := query.NewUpdateBuilder(s.store.Renderer(), libraryMemberTable).Set("role", role).Set("payload_json", conversationJSON(member)).Where(predicate).Build()
				if e = conversationExec(ctx, tx, q, args, e); e != nil {
					return e
				}
			} else {
				q, args, e := query.NewInsertBuilder(s.store.Renderer(), libraryMemberTable).Columns("scope_key", "library_id", "user_id", "role", "payload_json").Values(libraryScope(a), id, user, role, conversationJSON(member)).Build()
				if e = conversationExec(ctx, tx, q, args, e); e != nil {
					return e
				}
			}
		}
		if user == a.UserID {
			out.Role = role
		}
		return nil
	})
	return
}

var _ persistence.KnowledgeLibraryRepository = (*ConversationStore)(nil)
