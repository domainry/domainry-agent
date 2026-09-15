package main

import (
	"encoding/json"
	"os"
	"sort"

	agent "github.com/domainry/domainry-agent-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

func main() {
	personal, schedule := agent.PersonalConversationTools(), tools.ScheduleDefinitions()
	p, _ := json.Marshal(personal)
	s, _ := json.Marshal(schedule)
	unique := map[string]agent.ConversationToolDefinition{}
	for _, group := range [][]agent.ConversationToolDefinition{personal, schedule} {
		for _, definition := range group {
			unique[definition.Key] = definition
		}
	}
	keys := []string{}
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	combined := []agent.ConversationToolDefinition{}
	for _, key := range keys {
		combined = append(combined, unique[key])
	}
	raw, _ := json.Marshal(combined)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]int{"personal_tools": len(personal), "personal_definition_bytes": len(p), "schedule_tools": len(schedule), "schedule_definition_bytes": len(s), "unique_combined_tools": len(combined), "combined_definition_bytes": len(raw)}); err != nil {
		panic(err)
	}
}
