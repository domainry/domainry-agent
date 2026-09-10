package agent

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"testing"
)

func TestStoredToolResultReferenceOwnershipDigestAndDeletion(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	_, request := personalMutationFixture(t, repo, "result-owner", "memory_save", `{"title":"工作","content":"保留完整内容","enabled":true,"expected_revision":0}`, true)
	result, err := repo.ApplyPersonalTool(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	ref := agentsdk.ConversationResultReference{ConversationID: request.ConversationID, RunID: request.RunID, Step: request.Step, CallID: request.Call.ID, SHA256: conversationHash(result)}
	stored, err := repo.ConversationResult(t.Context(), ref, request.Authority)
	if err != nil || stored.Result == nil || conversationHash(stored.Result) != conversationHash(result) {
		t.Fatal("full result not readable", err)
	}
	for _, scope := range []string{"user", "workspace", "runtime"} {
		other := request.Authority
		switch scope {
		case "user":
			other.UserID = "other"
		case "workspace":
			other.WorkspaceID = "other"
		case "runtime":
			other.RuntimeID = "other"
		}
		_, err = repo.ConversationResult(t.Context(), ref, other)
		requireConversationCode(t, err, "result_not_found")
	}
	bad := ref
	bad.SHA256 = conversationHash("different")
	_, err = repo.ConversationResult(t.Context(), bad, request.Authority)
	requireConversationCode(t, err, "result_reference_changed")
	bad = ref
	bad.Step++
	_, err = repo.ConversationResult(t.Context(), bad, request.Authority)
	requireConversationCode(t, err, "result_not_found")
	// A read does not require a live worker lease; cancelled/finished results
	// remain available until their source conversation is actually deleted.
	if _, err = repo.Cancel(t.Context(), request.ConversationID, request.RunID, request.Authority); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ConversationResult(t.Context(), ref, request.Authority); err != nil {
		t.Fatal(err)
	}
	c, err := repo.Get(t.Context(), request.ConversationID, request.Authority)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(t.Context(), c.ID, c.Revision, request.Authority); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ConversationResult(t.Context(), ref, request.Authority)
	requireConversationCode(t, err, "result_not_found")
}
