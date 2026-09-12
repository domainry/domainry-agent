package product

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	transport "github.com/domainry/domainry-agent/internal/transport/http/product"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	toolsmodule "github.com/domainry/domainry-tools/module"

	"os"
)

// ConfigureRecords is reusable assembly, not a domain model. Each product
// supplies its validation, vocabulary, prompts, Skills and allowed tool subset.
func ConfigureRecords(key string, ui any, agentJSON, skillsJSON []byte, specs ...toolsmodule.RecordSpec) (Product, error) {
	p := Product{Key: key, UI: ui, CalendarTools: true, MailTools: true, WebTools: true, CalendarWriteTools: true, MailWriteTools: true, ReportTools: true, AnalysisTools: true, ScheduleTools: true}
	if path := os.Getenv("SAAS_AGENT_CONFIG"); path != "" {
		var err error
		agentJSON, err = os.ReadFile(path)
		if err != nil {
			return p, err
		}
	}
	if path := os.Getenv("SAAS_SKILLS_CONFIG"); path != "" {
		var err error
		skillsJSON, err = os.ReadFile(path)
		if err != nil {
			return p, err
		}
	}
	if len(agentJSON) > 131072 || len(skillsJSON) > 262144 {
		return p, fmt.Errorf("Agent configuration exceeds size limit")
	}
	if err := json.Unmarshal(agentJSON, &p.Agent); err != nil {
		return p, err
	}
	if err := json.Unmarshal(skillsJSON, &p.Skills); err != nil {
		return p, err
	}
	keys := []string{}
	for _, spec := range specs {
		for _, d := range toolsmodule.RecordDefinitions(spec) {
			p.Tools = append(p.Tools, d)
			keys = append(keys, d.Key)
		}
	}
	p.Prepare = func(ctx context.Context, h *Host) (Binding, error) {
		var resources []*toolsmodule.RecordAdapter
		store, err := knowledgemodule.NewRecordStore(ctx, knowledgemodule.SQLBackend{DB: h.Database(), Dialect: h.Dialect(), Engine: h.DatabaseProfile()}, h.Migrations(), key)
		if err != nil {
			return Binding{}, err
		}
		for _, spec := range specs {
			resources = append(resources, &toolsmodule.RecordAdapter{Store: store, Spec: spec, Authorize: h.AuthorizeConversationTool})
		}
		binding := Binding{Handler: transport.SetupRoutes(h.Identity, h.RuntimeID(), h.AuthorizeConversationTool, key, p.Tools, transport.RecordRoutes(h.RuntimeID(), resources...))}
		binding.AssembleTools = func(base sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
			reg := toolsmodule.NewRegistry()
			for _, a := range resources {
				if err := a.Register(reg); err != nil {
					return nil, err
				}
			}
			selected, err := reg.Select(keys)
			if err != nil {
				return nil, err
			}
			return toolsmodule.Combine(base, selected, keys)
		}
		return binding, nil
	}
	return p, nil
}
