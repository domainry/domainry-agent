package agent

import (
	"strconv"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func conversationItemScope(authority agentsdk.ConversationAuthority, conversationID, kind string) query.Predicate {
	return query.And(conversationScope(authority, conversationID), query.Equal("item_kind", kind))
}

func conversationRunEventItemKey(runID string, sequence int64) string {
	return runID + ":" + strconv.FormatInt(sequence, 10)
}

func conversationTaskItemReference(taskID, clientID string) string {
	return taskID + ":" + clientID
}

func conversationTaskCompletionItemKey(taskID string, revision int64) string {
	return taskID + ":" + strconv.FormatInt(revision, 10)
}
