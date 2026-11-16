package execution

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"testing"
)

func TestDiagnosticReadPlanFitsCallsButMustAlsoFitCost(t *testing.T) {
	// Three source gets, then policy, links, batched messages and six evidence
	// pages: exactly 12 calls but 21 weighted units. A call-only hint is wrong.
	plan := Usage{Calls: 12, Cost: 3*TaskToolCostUnits(agentsdk.AgentToolGetRecord) + 9*TaskToolCostUnits(agentsdk.AgentToolQueryRecords)}
	if _, err := (Budget{Calls: 12, Cost: TaskCostBudgetUnits("standard")}).Reserve(Usage{}, plan); err != ErrCostLimit {
		t.Fatal("underfunded diagnostic plan accepted", err)
	}
	if _, err := (Budget{Calls: 12, Cost: TaskCostBudgetUnits("high")}).Reserve(Usage{}, plan); err != nil {
		t.Fatal(err)
	}
}
