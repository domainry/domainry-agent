package definition

import (
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	"strings"
)

// Profile is an immutable runtime projection of a trusted Agent and its Skills.
// A Skill may narrow its instructions to tools selected by the Agent; it cannot grant tools.
type Profile struct {
	Key, Version, Instructions string
	Tools                      []string
	Limits                     sdk.AgentExecutionLimits
}

func CompileProfile(agent sdk.AgentSchema, skills []sdk.SkillSchema, available []string) (Profile, error) {
	var zero Profile
	if !stableDefinitionKey.MatchString(agent.Key) || agent.Name == "" || agent.Version == "" || strings.TrimSpace(agent.Instructions) == "" || len(agent.Instructions) > 32768 {
		return zero, fmt.Errorf("Agent key, version, name and bounded instructions are required")
	}
	if err := validateAgentExecutionLimits(agent.ExecutionLimits); err != nil {
		return zero, err
	}
	known := map[string]bool{}
	for _, key := range available {
		known[key] = true
	}
	selected := map[string]bool{}
	for _, key := range agent.Tools {
		if !known[key] || selected[key] {
			return zero, fmt.Errorf("unavailable or repeated Agent tool %q", key)
		}
		selected[key] = true
	}
	catalog := map[string]sdk.SkillSchema{}
	for _, skill := range skills {
		if !stableDefinitionKey.MatchString(skill.Key) || skill.Name == "" || skill.Version == "" || strings.TrimSpace(skill.Instructions) == "" || len(skill.Instructions) > 32768 {
			return zero, fmt.Errorf("invalid Skill %q", skill.Key)
		}
		if _, ok := catalog[skill.Key]; ok {
			return zero, fmt.Errorf("duplicate Skill %q", skill.Key)
		}
		catalog[skill.Key] = skill
	}
	prompt := agent.Instructions
	seen := map[string]bool{}
	for _, key := range agent.SkillKeys {
		skill, ok := catalog[key]
		if !ok || seen[key] {
			return zero, fmt.Errorf("missing or repeated Skill %q", key)
		}
		seen[key] = true
		for _, tool := range skill.AllowedTools {
			if !selected[tool] {
				return zero, fmt.Errorf("Skill %q requests unselected tool %q", key, tool)
			}
		}
		prompt += "\n\nSkill " + skill.Key + " @ " + skill.Version + ":\n" + skill.Instructions
	}
	if len(prompt) > 65536 {
		return zero, fmt.Errorf("combined Agent instructions exceed budget")
	}
	return Profile{Key: agent.Key, Version: agent.Version, Instructions: prompt, Tools: append([]string{}, agent.Tools...), Limits: agent.ExecutionLimits}, nil
}
