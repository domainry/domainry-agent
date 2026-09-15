package definition

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"strings"
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

func TestProfilePublishesOnlySkillSummaryAndValidatesWorkflow(t *testing.T) {
	a := sdk.AgentSchema{Key: "pm", Name: "PM", Version: "1", Instructions: "Base", Tools: []string{"read"}, SkillKeys: []string{"prd"}}
	skill := sdk.SkillSchema{Key: "prd", Version: "2", Name: "PRD", Description: "Draft a PRD", Instructions: "SECRET FULL BODY", AllowedTools: []string{"read"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`), Resources: []sdk.SkillResource{{Key: "template", Name: "Template", MediaType: "text/markdown", Content: "SECRET RESOURCE"}}, Workflow: []sdk.SkillWorkflowStep{{Key: "research", Name: "Research", Instructions: "Inspect sources", AllowedTools: []string{"read"}}, {Key: "draft", Name: "Draft", Instructions: "Write", DependsOn: []string{"research"}}}}
	p, err := CompileProfile(a, []sdk.SkillSchema{skill}, []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Instructions, "SECRET") || !strings.Contains(p.Instructions, "prd @ 2") || !strings.Contains(p.Instructions, "skill_load") {
		t.Fatalf("profile did not retain a summary-only directory: %q", p.Instructions)
	}
	skill.Workflow[0].DependsOn = []string{"draft"}
	if _, err = CompileProfile(a, []sdk.SkillSchema{skill}, []string{"read"}); err == nil {
		t.Fatal("cyclic workflow accepted")
	}
}
