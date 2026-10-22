package agent

import (
	"testing"

	"github.com/domainry/domainry-foundation/entityid"
)

func TestDerivedConversationIDIsCanonicalAndComponentFramed(t *testing.T) {
	first, err := derivedConversationID("agent.delegation-conversation", "owner-a", "client-a")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := derivedConversationID("agent.delegation-conversation", "owner-a", "client-a")
	if err != nil {
		t.Fatal(err)
	}
	combined, err := derivedConversationID("agent.delegation-conversation", "owner-a\x00client-a")
	if err != nil {
		t.Fatal(err)
	}
	if first != replay || first == combined {
		t.Fatalf("derived conversation IDs first=%q replay=%q combined=%q", first, replay, combined)
	}
	if err = entityid.Validate("conv", first); err != nil {
		t.Fatal(err)
	}
}
