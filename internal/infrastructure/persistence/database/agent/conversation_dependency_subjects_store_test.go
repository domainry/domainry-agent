package agent

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func nestedSubjectDependency(t *testing.T, repo *ConversationStore, parent sdk.ConversationDelegation, issuer, executor sdk.ConversationAuthority, sourceAgent sdk.ConversationAgentSnapshot, key string) sdk.ConversationDelegation {
	t.Helper()
	users, mode := []string{issuer.UserID}, "owner"
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: key, Name: key, Instructions: "Use admitted dependency", ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}, executor)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, OwnerUserID: executor.UserID, DelegationRoleKey: executor.RoleKey, ExecutionSubject: &sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: key, Deliverable: "Findings", CompletionConditions: []string{"Check evidence"}}
	d, err := repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{ExecutionAuthority: &executor, Request: sdk.ConversationDelegationCreate{ClientID: key, ConversationID: parent.ConversationID, AgentID: agent.ID, Purpose: key, Brief: brief}, FromAgentID: parent.ToAgentID, SourceAgent: sourceAgent, Agent: snapshot, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: parent.ConversationID, Budget: parent.Budget, MaxInputBytes: 32768}}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCrossOwnerContractRepublicationKeepsUpstreamPublisherAndRecipientAudience(t *testing.T) {
	repo, a, b, in := delegationSubjectFixture(t)
	original := a
	original.RoleKey = "original-upstream-source-role"
	source, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "upstream-original-source"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), source.ID, sdk.ConversationSend{ClientMessageID: "upstream-original-source", Message: "Exact original requirements"}, original)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), original.RuntimeID, "upstream-source-worker", time.Minute)
	if err != nil || !found || claim.Authority != original || claim.Run.ID != run.ID {
		t.Fatal("original source execution claim", claim, found, err)
	}
	in.Request.ConversationID, in.Task.SourceConversationID, in.SourceRunID = source.ID, source.ID, run.ID
	var definition sdk.ConversationToolDefinition
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "agent_delegate" {
			definition = tool
		}
	}
	input := executionStoreInput()
	input.Tools = []sdk.ConversationToolDefinition{definition}
	var arguments map[string]json.RawMessage
	if err := unmarshalDurableJSON(conversationJSON(in.Request), &arguments); err != nil {
		t.Fatal(err)
	}
	delete(arguments, "client_id")
	delete(arguments, "conversation_id")
	call := sdk.ConversationToolCall{ID: "original-delegation", Name: definition.Key, Arguments: string(conversationJSON(arguments))}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	confirmation, err := repo.WaitExecution(t.Context(), claim, persistence.ConversationWait{Step: 0, CallID: call.ID, Kind: "confirmation", Question: "明确发布本次原要求？"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RespondExecution(t.Context(), source.ID, run.ID, sdk.ConversationInteractionResponse{InteractionID: confirmation.ID, ClientID: "approve-original-delegation", ExpectedRevision: confirmation.Revision, Decision: "approve"}, original); err != nil {
		t.Fatal(err)
	}
	claim, found, err = repo.Claim(t.Context(), original.RuntimeID, "confirmed-source-worker", time.Minute)
	if err != nil || !found || claim.Authority != original {
		t.Fatal("confirmed original source claim", claim, found, err)
	}
	ledger, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true})
	if err != nil {
		t.Fatal(err)
	}
	in.Request.ToolRequest = &sdk.ConversationToolRequest{Authority: original, ConversationID: source.ID, RunID: run.ID, Step: 0, Call: call, Definition: definition, IdempotencyKey: ledger.IdempotencyKey, LeaseOwner: claim.Owner, Fence: claim.Fence}
	root, err := repo.CreateConversationDelegation(t.Context(), in, original)
	if err != nil || root.BriefSource == nil {
		t.Fatal("original upstream source was not frozen", root, err)
	}
	if err := repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolResult{Status: "completed", ResourceID: root.ID, Content: conversationJSON(root)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Original scope recorded"}, ""); err != nil {
		t.Fatal(err)
	}
	c := a
	c.UserID, c.RoleKey = "downstream-executor", "downstream-execution-role"
	root = dependencyViewers(t, repo, a, root, c.UserID)
	child := nestedSubjectDependency(t, repo, root, b, c, in.Agent, "dependency-republication")
	if len(child.Dependencies) != 1 || child.Dependencies[0].Source == nil || *child.Dependencies[0].Source != *root.BriefSource {
		t.Fatal("cross-owner dependency lost exact original source", child.Dependencies)
	}
	record, err := repo.ConversationContractPublicationRecord(t.Context(), child.ID, 1, b)
	if err != nil {
		t.Fatal(err)
	}
	upstreamProof := func() string {
		t.Helper()
		q, args, err := query.NewSelectBuilder(repo.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.Equal("delegation_id", root.ID)).OrderBy(query.Ascending("release_id")).Build()
		rows, err := repo.store.Database().QueryContext(t.Context(), q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var proof [][]byte
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			proof = append(proof, raw)
		}
		if err := rows.Err(); err != nil || len(proof) == 0 {
			t.Fatal("missing actual upstream publication", proof, err)
		}
		return conversationHash(proof)
	}
	before := upstreamProof()
	update := sdk.ConversationDelegationUpdate{ClientID: "republish-downstream-contract", ExpectedRevision: child.Revision, Action: "republish_contract", Reason: "Explicitly publish own original agreement", ContractPublication: &sdk.ConversationContractPublication{AgreementRevision: 1, RecordDigest: conversationHash(record)}}
	child, err = repo.UpdateConversationDelegation(t.Context(), child.ID, update, b)
	if err != nil || upstreamProof() != before {
		t.Fatal("downstream republication rewrote upstream source/publisher", child, err)
	}
	q, args, err := query.NewSelectBuilder(repo.store.Renderer(), conversationSourceReleaseTable).Projections(query.Project(query.CountAll())).Where(query.Equal("delegation_id", child.ID)).Build()
	var releases int
	if err = repo.store.Database().QueryRowContext(t.Context(), q, args...).Scan(&releases); err != nil || releases != 0 {
		t.Fatal("downstream owner published upstream private sources", releases, err)
	}
	dependencyViewers(t, repo, a, root)
	update.ClientID, update.ExpectedRevision = "republish-withdrawn-upstream-audience", child.Revision
	if _, err := repo.UpdateConversationDelegation(t.Context(), child.ID, update, b); err == nil {
		t.Fatal("republication restored recipient's withdrawn upstream audience")
	}
	if current := currentPeer(t, repo, b, child.ID); current.Revision != child.Revision {
		t.Fatal("denied republication mutated current agreement", current)
	}
}

func dependencyViewers(t *testing.T, repo *ConversationStore, issuer sdk.ConversationAuthority, d sdk.ConversationDelegation, users ...string) sdk.ConversationDelegation {
	t.Helper()
	grants := []sdk.ConversationDelegationParticipantInput{}
	for _, user := range users {
		grants = append(grants, sdk.ConversationDelegationParticipantInput{UserID: user, Operations: []string{"view"}})
	}
	d = currentPeer(t, repo, issuer, d.ID)
	next, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "view-" + conversationHash(grants)[:20], ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestCrossOwnerDependencyChangeStopsTransitiveOriginalWorkersAndPreservesRealm(t *testing.T) {
	repo, a, b, in := delegationSubjectFixture(t)
	root, err := repo.CreateConversationDelegation(t.Context(), in, a)
	if err != nil {
		t.Fatal(err)
	}
	c, d := a, a
	c.UserID, c.RoleKey = "analyst", "analyst-original"
	d.UserID, d.RoleKey = "writer", "writer-original"
	root = dependencyViewers(t, repo, a, root, c.UserID, d.UserID)
	child := nestedSubjectDependency(t, repo, root, b, c, in.Agent, "cross-owner-child")
	child = dependencyViewers(t, repo, b, child, d.UserID)
	childTask, err := repo.ConversationTask(t.Context(), child.TaskID, c)
	if err != nil {
		t.Fatal(err)
	}
	grandchild := nestedSubjectDependency(t, repo, child, c, d, *childTask.Agent, "cross-owner-grandchild")
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		direct := append([]sdk.ConversationTaskDependency{}, grandchild.Dependencies...)
		direct = append(direct, child.Dependencies...)
		flattened, err := repo.flattenTaskDependencies(t.Context(), tx, direct, d)
		if err != nil {
			return err
		}
		if len(flattened) != 2 {
			t.Fatal("omitted and empty field scopes duplicated the same adopted dependency", flattened)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Matching uses only agent IDs from the original identity chain. The
	// current executor never acquires the upstream private conversations.
	ancestors, err := repo.ConversationAgentAncestors(t.Context(), grandchild.ConversationID, d)
	if err != nil {
		t.Fatal("cross-owner matching ancestry", err)
	}
	seen := map[string]bool{}
	for _, id := range ancestors {
		seen[id] = true
	}
	for _, id := range []string{root.FromAgentID, root.ToAgentID, child.ToAgentID, grandchild.ToAgentID} {
		if !seen[id] {
			t.Fatal("matching lost an original upstream agent ID", id, ancestors)
		}
	}
	if _, err := repo.Get(t.Context(), root.SourceConversationID, d); err == nil {
		t.Fatal("matching ancestry granted upstream private conversation access")
	}
	if ids, err := repo.ConversationAgentAncestors(t.Context(), grandchild.ConversationID, a); err == nil || len(ids) != 0 {
		t.Fatal("ancestry leaked through the wrong initial owner", ids, err)
	}
	wrongRealm := d
	wrongRealm.WorkspaceID = "another-workspace"
	if ids, err := repo.ConversationAgentAncestors(t.Context(), grandchild.ConversationID, wrongRealm); err == nil || len(ids) != 0 {
		t.Fatal("ancestry crossed the original workspace", ids, err)
	}
	// Same opaque goal key in another workspace must not enter invalidation.
	foreign := a
	foreign.WorkspaceID = "another-workspace"
	foreign.RoleKey = "foreign-role"
	foreignSource, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "foreign-source"}, foreign)
	if err != nil {
		t.Fatal(err)
	}
	foreignAgent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "foreign", Name: "Foreign", Instructions: "Independent", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, foreign)
	if err != nil {
		t.Fatal(err)
	}
	foreignWork, err := repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "foreign", ConversationID: foreignSource.ID, AgentID: foreignAgent.ID, Purpose: "Foreign", Brief: root.Brief}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: foreignAgent.ID, Revision: foreignAgent.Revision}, Task: sdk.ConversationTask{Goal: root.Brief.Goal, SourceConversationID: foreignSource.ID, Budget: root.Budget}}, foreign)
	if err != nil {
		t.Fatal(err)
	}
	foreignWork.RootConversationID = root.RootConversationID
	foreignWork.Dependencies = child.Dependencies
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		q, args, e := query.NewUpdateBuilder(repo.store.Renderer(), conversationPeerLinkTable).Set("root_conversation_id", root.RootConversationID).Set("payload_json", conversationJSON(foreignWork)).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(foreign)), query.Equal("link_id", foreignWork.ID))).Build()
		return conversationExec(t.Context(), tx, q, args, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]persistence.ConversationClaim{}
	for range 4 {
		launch, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
		if err != nil || !found {
			t.Fatal(launch, found, err)
		}
		claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "dependency-worker", time.Minute)
		if err != nil || !found {
			t.Fatal(claim, found, err)
		}
		input := executionStoreInput()
		if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
			t.Fatal(err)
		}
		claims[claim.Run.BackgroundTask.DelegationID] = claim
	}
	root = currentPeer(t, repo, a, root.ID)
	brief := root.Brief
	brief.Version++
	brief.Goal = "Changed source agreement"
	_, err = repo.UpdateConversationDelegation(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: "cross-owner-change", ExpectedRevision: root.Revision, Action: "update_brief", Reason: "New goal", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	for _, item := range []struct {
		work            sdk.ConversationDelegation
		owner, executor sdk.ConversationAuthority
	}{{child, b, c}, {grandchild, c, d}} {
		current := currentPeer(t, repo, item.owner, item.work.ID)
		if current.Status != "needs_update" || len(current.PendingChanges) != 1 || current.PendingChanges[0].SourceDelegationID != root.ID {
			t.Fatal("cross-owner dependency did not invalidate", current)
		}
		if claims[item.work.ID].Authority != item.executor {
			t.Fatal("wrong original execution role", claims[item.work.ID].Authority)
		}
		if err := repo.Finish(t.Context(), claims[item.work.ID], sdk.ConversationModelResult{Content: "stale"}, ""); err == nil {
			t.Fatal("stale cross-owner worker committed")
		}
		task, err := repo.ConversationTask(t.Context(), item.work.TaskID, item.executor)
		if err != nil || task.Status != "cancelled" {
			t.Fatal("original worker not stopped", task, err)
		}
		if _, err := repo.ConversationDelegation(t.Context(), item.work.ID, a); err == nil {
			t.Fatal("invalidation granted private dependent access")
		}
		notices, err := repo.ConversationAgentMessages(t.Context(), item.work.ID, item.owner)
		if err != nil || len(notices) != 2 {
			t.Fatal("original owner notices missing", notices, err)
		}
	}
	if task, err := repo.ConversationTask(t.Context(), foreignWork.TaskID, foreign); err != nil || task.Status != "running" {
		t.Fatal("another realm changed", task, err)
	}
}
