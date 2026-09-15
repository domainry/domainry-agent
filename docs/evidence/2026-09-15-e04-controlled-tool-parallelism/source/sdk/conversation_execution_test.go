package agentsdk

import (
	"testing"

	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestConversationParallelReadWidthRequiresExplicitIndependentReads(t *testing.T) {
	read := ConversationToolDefinition{Key: "read", Effect: "read", Parallelism: toolsdk.ToolParallelismIndependentRead}
	other := ConversationToolDefinition{Key: "other", Effect: "read", Parallelism: toolsdk.ToolParallelismIndependentRead}
	serial := ConversationToolDefinition{Key: "serial", Effect: "read"}
	write := ConversationToolDefinition{Key: "write", Effect: "write", Parallelism: toolsdk.ToolParallelismIndependentRead}
	calls := []ConversationToolCall{{ID: "one", Name: "read"}, {ID: "two", Name: "other"}, {ID: "three", Name: "read"}}
	if width := ConversationParallelReadWidth([]ConversationToolDefinition{read, other}, calls, 2); width != 2 {
		t.Fatalf("bounded width=%d", width)
	}
	if width := ConversationParallelReadWidth([]ConversationToolDefinition{read, other}, calls, 8); width != 3 {
		t.Fatalf("full width=%d", width)
	}
	for name, definitions := range map[string][]ConversationToolDefinition{
		"legacy serial": {read, serial},
		"write":         {read, write},
		"missing":       {read},
	} {
		candidate := calls[:2]
		if name == "legacy serial" {
			candidate[1].Name = "serial"
		} else if name == "write" {
			candidate[1].Name = "write"
		}
		if width := ConversationParallelReadWidth(definitions, candidate, 2); width != 1 {
			t.Fatalf("%s width=%d", name, width)
		}
		candidate[1].Name = "other"
	}
	if width := ConversationParallelReadWidth([]ConversationToolDefinition{read, other}, calls, 1); width != 1 {
		t.Fatalf("disabled width=%d", width)
	}
}

func TestPersonalPureReadsExplicitlyDeclareParallelSafety(t *testing.T) {
	parallel := map[string]bool{}
	for _, definition := range PersonalConversationTools() {
		if definition.Parallelism == toolsdk.ToolParallelismIndependentRead {
			parallel[definition.Key] = true
			if definition.Effect != "read" {
				t.Fatalf("write marked parallel: %+v", definition)
			}
		}
	}
	if !parallel["time_now"] || !parallel["calculate"] || parallel["ask_user"] || parallel["memory_save"] {
		t.Fatalf("personal declarations=%v", parallel)
	}
}
