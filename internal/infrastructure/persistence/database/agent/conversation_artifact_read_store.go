package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type artifactCursor struct {
	Owner, Query, ID string
	Created, Cutoff  int64
}

func (s *ConversationStore) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	out := agentsdk.ConversationArtifactPage{Items: []agentsdk.ConversationArtifact{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if !executionText(in.Query, 256, false) || len(in.Cursor) > 2048 || in.Limit < 1 || in.Limit > 50 || in.SourceConversationID != "" && !personalMemoryKey(in.SourceConversationID) {
		return out, conversationError("bad_request", "artifact_query_invalid")
	}
	key := in
	key.Cursor = ""
	owner, hash := conversationOwner(a), conversationHash(key)
	cursor := artifactCursor{Owner: owner, Query: hash, Cutoff: time.Now().UnixMilli()}
	filters := []query.Predicate{query.Equal("owner_key", owner)}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || !personalMemoryKey(cursor.ID) || cursor.Created < 1 || cursor.Cutoff < cursor.Created {
			return out, conversationError("bad_request", "artifact_cursor_invalid")
		}
		filters = append(filters, query.Or(query.LessThan("created_at", cursor.Created), query.And(query.Equal("created_at", cursor.Created), query.LessThan("artifact_id", cursor.ID))))
	}
	filters = append(filters, query.LessThanOrEqual("created_at", cursor.Cutoff))
	if in.SourceConversationID != "" {
		filters = append(filters, query.Equal("source_conversation_id", in.SourceConversationID))
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_artifacts").Columns("payload_json").Where(query.And(filters...)).OrderBy(query.Descending("created_at"), query.Descending("artifact_id")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			out.Complete = false
			break
		}
		var raw []byte
		var item agentsdk.ConversationArtifact
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		scanned++
		cursor.ID, cursor.Created = item.ID, item.CreatedAt.UnixMilli()
		if strings.Contains(strings.ToLower(item.Title), strings.ToLower(in.Query)) {
			out.Items = append(out.Items, item)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if !out.Complete {
		out.NextCursor = base64.RawURLEncoding.EncodeToString(conversationJSON(cursor))
	}
	return out, nil
}

func (s *ConversationStore) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	out := agentsdk.ConversationArtifactVersions{Items: []agentsdk.ConversationArtifact{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if before < 0 || limit < 0 || limit > 50 {
		return out, conversationError("bad_request", "artifact_query_invalid")
	}
	if limit == 0 {
		limit = 20
	}
	head, err := s.artifactHead(ctx, s.store.Database(), id, a)
	if err != nil {
		return out, err
	}
	if before == 0 || before > head.Version+1 {
		before = head.Version + 1
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_artifact_versions").Columns("payload_json").Where(query.And(artifactScope(a, id), query.LessThan("version", before))).OrderBy(query.Descending("version")).Limit(limit + 1).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Items) == limit {
			out.Complete = false
			out.NextBefore = out.Items[len(out.Items)-1].Version
			break
		}
		var raw []byte
		var item persistence.ConversationArtifactRecord
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item.Artifact)
	}
	return out, rows.Err()
}
