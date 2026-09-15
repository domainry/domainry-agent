package agent

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func TestSameIdentityDelegationControlsPreserveOriginalExecutionRoleAndHistoricalRun(t *testing.T) {
	repo, original, _, admission := delegationSubjectFixture(t, true)
	admission.ExecutionAuthority = &original
	d, err := repo.CreateConversationDelegation(t.Context(), admission, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.delegationSubjects(t.Context(), repo.store.Database(), d.ID, original); err != nil || found {
		t.Fatal("expected an unmapped same-identity admission", found, err)
	}
	manager := original
	manager.RoleKey = "another-management-role"
	control := func(id, action string, brief *sdk.ConversationTaskBrief) {
		t.Helper()
		d, err = repo.ConversationDelegation(t.Context(), d.ID, manager)
		if err != nil {
			t.Fatal(err)
		}
		d, err = NewConversationStore(repo.store).UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: id, Action: action, ExpectedRevision: d.Revision, Reason: "Manage original accepted work", Brief: brief}, manager)
		if err != nil {
			t.Fatal(action, err, "input", id, "manager", manager)
		}
		subjects, err := NewConversationStore(repo.store).ConversationDelegationAuthorities(t.Context(), d.ID, manager)
		if err != nil || subjects.Executor != original || subjects.Issuer != original {
			t.Fatal("management role replaced immutable accepted authorities", action, subjects, err)
		}
	}
	control("pause-original", "pause", nil)
	control("resume-original", "resume", nil)
	old, found, err := repo.LaunchConversationTask(t.Context(), original.RuntimeID)
	if err != nil || !found || old.Authority != original || old.Run.BackgroundTask.AgreementRevision != 1 {
		t.Fatal("first resumed launch changed accepted role", old, found, err)
	}
	control("pause-launched-original", "pause", nil)
	brief := d.Brief
	brief.Version++
	brief.Goal += " with revised requirements"
	control("update-original", "update_brief", &brief)
	control("resume-revised-original", "resume", nil)
	current, found, err := NewConversationStore(repo.store).LaunchConversationTask(t.Context(), original.RuntimeID)
	if err != nil || !found || current.Authority != original || current.Run.BackgroundTask.AgreementRevision != 2 || current.Run.BackgroundTask.BriefVersion != 2 {
		t.Fatal("revised launch changed accepted role or used old requirements", current, found, err)
	}
	historical, err := repo.Run(t.Context(), old.Run.ConversationID, old.Run.ID, original)
	if err != nil || historical.BackgroundTask.AgreementRevision != 1 || historical.BackgroundTask.BriefVersion != 1 {
		t.Fatal("revised launch replaced historical admitted agreement", historical, err)
	}
}

func TestDelegationRequirementInboxLaunchPreservesAcceptedExecutionIdentity(t *testing.T) {
	for _, mode := range []string{"same-identity", "legacy-manager-header", "cross-user"} {
		t.Run(mode, func(t *testing.T) {
			repo, issuer, executor, admission := delegationSubjectFixture(t, mode != "cross-user")
			if mode != "cross-user" {
				executor = issuer
				admission.ExecutionAuthority = &executor
			}
			modeBinding := "owner"
			shared := []string{}
			if executor.UserID != issuer.UserID {
				shared = []string{issuer.UserID}
			}
			configured, err := repo.WriteConversationAgent(t.Context(), admission.Agent.ID, sdk.ConversationAgentWrite{ClientID: "bind-receiver", ExpectedRevision: admission.Agent.Revision, Name: "Original receiver", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1, DelegationExecution: &modeBinding, SharedWithUserIDs: &shared}, executor)
			if err != nil {
				t.Fatal(err)
			}
			admission.Agent.Revision, admission.Agent.DelegationRoleKey = configured.Revision, executor.RoleKey
			d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
			if err != nil {
				t.Fatal(err)
			}
			manager := issuer
			manager.RoleKey = "another-management-role"
			brief := d.Brief
			brief.Version++
			brief.Goal += " after an agreement change"
			for _, update := range []sdk.ConversationDelegationUpdate{
				{ClientID: "change", Action: "update_brief", Brief: &brief, Reason: "Update the accepted work"},
				{ClientID: "resume", Action: "resume", Reason: "Resume the revised work"},
			} {
				update.ExpectedRevision = d.Revision
				d, err = NewConversationStore(repo.store).UpdateConversationDelegation(t.Context(), d.ID, update, manager)
				if err != nil {
					t.Fatal(update.Action, err)
				}
			}
			if mode == "legacy-manager-header" {
				// A queued notice from the old implementation recorded the manager's
				// role. Restart must resolve the admitted task, not execute as manager.
				q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAgentMessageTable).Set("authority_json", conversationJSON(manager)).Where(query.And(query.Equal("owner_key", conversationOwner(executor)), query.Equal("conversation_id", d.ConversationID))).Build()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = repo.store.Database().ExecContext(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			foundReceiver := false
			for attempt := 0; attempt < 2; attempt++ {
				run, found, err := NewConversationStore(repo.store).LaunchConversationPeerMessage(t.Context(), issuer.RuntimeID)
				if err != nil || !found {
					t.Fatal("requirement notice did not launch", found, err)
				}
				expected := issuer
				if run.ConversationID == d.ConversationID {
					expected, foundReceiver = executor, true
					if run.Agent == nil || run.Agent.DelegationRoleKey != executor.RoleKey || run.BackgroundTask == nil || run.BackgroundTask.AgreementRevision != 2 || run.BackgroundTask.BriefVersion != 2 {
						t.Fatal("inbox replaced the accepted snapshot or agreement", run)
					}
				} else if run.ConversationID != d.SourceConversationID {
					t.Fatal("notice launched an unrelated conversation", run)
				}
				row, err := repo.runRow(t.Context(), repo.store.Database(), run.ConversationID, run.ID, expected)
				if err != nil || row.Authority != expected {
					t.Fatal("inbox launched under the manager's identity", row.Authority, expected, err)
				}
				if foundReceiver {
					break
				}
			}
			if !foundReceiver {
				t.Fatal("receiver's requirement notice was not launched")
			}
			if _, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID); err != nil || found {
				t.Fatal("ordinary launcher duplicated the inbox execution", found, err)
			}
		})
	}
}
