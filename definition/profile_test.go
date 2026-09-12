package definition

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"testing"
)

func TestProfileCannotExpandToolsThroughSkills(t *testing.T) {
	a := sdk.AgentSchema{Key: "pm", Name: "PM", Version: "1", Instructions: "Product manager", Tools: []string{"read"}, SkillKeys: []string{"prd"}}
	skills := []sdk.SkillSchema{{Key: "prd", Version: "1", Name: "PRD", Instructions: "Write requirements", AllowedTools: []string{"write"}}}
	if _, e := CompileProfile(a, skills, []string{"read", "write"}); e == nil {
		t.Fatal("Skill expanded Agent privileges")
	}
	skills[0].AllowedTools = []string{"read"}
	p, e := CompileProfile(a, skills, []string{"read"})
	if e != nil {
		t.Fatal(e)
	}
	a.Tools[0] = "write"
	if p.Tools[0] != "read" {
		t.Fatal("profile not frozen")
	}
	a.Tools = []string{"missing"}
	if _, e = CompileProfile(a, skills, []string{"read"}); e == nil {
		t.Fatal("unregistered tool selected")
	}
}
