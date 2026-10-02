package module

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestBindingAuthorizationActionsIncludeBoundProductConversationTools(t *testing.T) {
	actions, err := agentsdk.ConversationToolAuthorizationActions([]agentsdk.ConversationToolDefinition{{
		Key: "crm_search_accounts", ActionKey: agentsdk.ConversationToolActionPrefix + "crm_search_accounts", Effect: "read", Idempotency: "natural",
	}})
	if err != nil {
		t.Fatal(err)
	}
	moduleBinding := &binding{conversationToolActions: actions}
	definitions, err := moduleBinding.AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, definition := range definitions {
		if definition.Key != agentsdk.ConversationToolActionPrefix+"crm_search_accounts" {
			continue
		}
		found = definition.Permission != nil && definition.Permission.Key == definition.Key && definition.OperationKey == "crm_search_accounts"
	}
	if !found {
		t.Fatal("bound product conversation tool Action was not published")
	}
}

func TestBindingAuthorizationActionsDeduplicateExactStaticToolProjection(t *testing.T) {
	static := agentsdk.ConversationToolActions()
	if len(static) == 0 {
		t.Fatal("static conversation tool catalog is empty")
	}
	moduleBinding := &binding{conversationToolActions: []actioncontract.ActionDefinition{static[0]}}
	definitions, err := moduleBinding.AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, definition := range definitions {
		if definition.Key == static[0].Key {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("static Action projection count=%d", count)
	}

	conflict := actioncontract.CloneDefinition(static[0])
	conflict.Label = "conflicting label"
	moduleBinding = &binding{conversationToolActions: []actioncontract.ActionDefinition{conflict}}
	if _, err := moduleBinding.AuthorizationActions(); err == nil {
		t.Fatal("conflicting static and bound product Action definitions were accepted")
	}
}
