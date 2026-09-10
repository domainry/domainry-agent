package integration_test

import (
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestPersonalHistorySearchOriginalReferencesPagingAndOwnership(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	other := a
	other.UserID = "other"
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"发布计划：保留原文，不依赖摘要。", "第二轮发布计划，截止时间周五。"} {
		key := []string{"first", "second"}[i]
		_, err = repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: key, Message: text}, a)
		if err != nil {
			t.Fatal(err)
		}
		claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "history-worker", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
		if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "已讨论。"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	query := agentsdk.ConversationHistorySearch{Query: "发布计划", Limit: 1, After: time.Now().Add(-time.Hour).Format(time.RFC3339), Before: time.Now().Add(time.Hour).Format(time.RFC3339)}
	seen := map[string]bool{}
	for page := 0; page < 6; page++ {
		result, err := repo.SearchHistory(t.Context(), query, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range result.Items {
			if seen[hit.MessageID] {
				t.Fatal("duplicate cursor result")
			}
			seen[hit.MessageID] = true
			original, err := repo.HistoryMessage(t.Context(), hit.ConversationID, hit.MessageID, a)
			if err != nil || original.Seq != hit.Seq || original.Content == "" {
				t.Fatalf("invalid reference %+v %v", original, err)
			}
			if _, err = repo.HistoryMessage(t.Context(), hit.ConversationID, hit.MessageID, other); err == nil {
				t.Fatal("cross-user original read")
			}
		}
		if result.Complete {
			break
		}
		if result.NextCursor == "" {
			t.Fatal("incomplete search without cursor")
		}
		query.Cursor = result.NextCursor
		if _, err = repo.SearchHistory(t.Context(), query, other); err == nil {
			t.Fatal("cross-user cursor accepted")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("matched %d originals", len(seen))
	}
	result, err := repo.SearchHistory(t.Context(), agentsdk.ConversationHistorySearch{Query: "发布计划"}, other)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("cross-user history %+v %v", result, err)
	}
	result, err = repo.SearchHistory(t.Context(), agentsdk.ConversationHistorySearch{Query: "发布计划", Before: time.Now().Add(-time.Hour).Format(time.RFC3339)}, a)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("date scope %+v %v", result, err)
	}
	if _, err = repo.SearchHistory(t.Context(), agentsdk.ConversationHistorySearch{Query: "发布计划", ConversationID: conversation.ID}, other); err == nil {
		t.Fatal("cross-user conversation scope accepted")
	}
}
