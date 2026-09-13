package execution

import (
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	"reflect"
	"testing"
)

func TestBriefProjectionTracksSelectedRequirements(t *testing.T) {
	a := sdk.ConversationTaskBrief{Version: 1, Goal: "Check", Constraints: []string{"EUR"}, Audience: "Finance"}
	b := a
	b.Version = 2
	b.Audience = "Engineering"
	before, err := BriefProjection(a, []string{"constraints"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := BriefProjection(b, []string{"constraints"})
	if err != nil || string(before) != string(after) {
		t.Fatal("irrelevant field changed dependency")
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(before, &values) != nil || len(values) != 1 {
		t.Fatal("projection leaked fields")
	}
	if fields := ChangedBriefFields(a, b); !reflect.DeepEqual(fields, []string{"audience"}) {
		t.Fatal(fields)
	}
	for _, fields := range [][]string{{"unknown"}, {"constraints", "constraints"}, {"version"}} {
		if _, err = BriefProjection(a, fields); err == nil {
			t.Fatal("invalid field", fields)
		}
	}
}
