package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type discoveryModel struct{}

func (discoveryModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{}, nil
}
func (discoveryModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Model: "review", Fingerprint: "v1"}
}
func (discoveryModel) StreamConversationStep(context.Context, sdk.ConversationStepRequest, func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	return sdk.ConversationStepResult{}, nil
}

type discoveryHost struct {
	sdk.ConversationToolHost
	denied map[string]bool
}

func (h discoveryHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	var out []sdk.ConversationToolDefinition
	for _, d := range sdk.PersonalConversationTools() {
		if d.Key == "time_now" || d.Key == "calculate" {
			out = append(out, d)
		}
	}
	return out, nil
}
func (h discoveryHost) AuthorizeConversationTool(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: !h.denied[in.Definition.Key]}, nil
}

type discoveryRepo struct {
	privatePeerSources
	agents       []sdk.ConversationAgent
	observations persistence.ConversationAgentObservations
}

func (r *discoveryRepo) ConversationAgents(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationAgent, error) {
	return r.agents, nil
}
func (r *discoveryRepo) ConversationAgent(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.ConversationAgent, error) {
	for _, a := range r.agents {
		if a.ID == id {
			return a, nil
		}
	}
	return sdk.ConversationAgent{}, conversationFailure("not_found", "agent_not_found")
}
func (r *discoveryRepo) ConversationAgentObservations(context.Context, sdk.ConversationAuthority) (persistence.ConversationAgentObservations, error) {
	return r.observations, nil
}
func (r *discoveryRepo) Get(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.Conversation, error) {
	if id != "source" {
		return sdk.Conversation{}, conversationFailure("not_found", "not_found")
	}
	return sdk.Conversation{ID: id, AgentID: "default"}, nil
}

func discoveryFixture() (*ConversationService, *discoveryRepo, discoveryHost, sdk.ConversationAuthority) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	r := &discoveryRepo{observations: persistence.ConversationAgentObservations{Load: map[string]persistence.ConversationAgentLoad{"busy": {Running: 1}}, HistoryComplete: true}}
	for _, id := range []string{"ready", "busy", "missing", "disabled"} {
		tools := []string{"calculate"}
		if id == "missing" {
			tools = []string{"time_now"}
		}
		r.agents = append(r.agents, sdk.ConversationAgent{ID: id, Name: id, Instructions: "Check the requested evidence", Tools: tools, ModelKey: "default", Enabled: id != "disabled", MaxConcurrent: 1, Revision: 1})
	}
	host := discoveryHost{denied: map[string]bool{}}
	s := &ConversationService{repo: r, model: discoveryModel{}, runtimeID: a.RuntimeID, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}, ToolHost: host, Workers: 2}}
	return s, r, host, a
}

func TestPeerDiscoveryRanksReadyPeersAndRechecksPermissions(t *testing.T) {
	s, _, host, a := discoveryFixture()
	request := sdk.ConversationAgentMatchRequest{ConversationID: "source", Requirements: sdk.ConversationAgentRequirements{Tools: []string{"calculate"}}}
	page, err := s.MatchConversationAgents(t.Context(), request, a)
	if err != nil || page.RecommendedAgentID != "ready" {
		t.Fatalf("match %+v %v", page, err)
	}
	states := map[string]string{}
	for _, item := range page.Items {
		states[item.AgentID] = item.State
		if item.CheckedAt.IsZero() {
			t.Fatal("missing freshness")
		}
	}
	if states["busy"] != "queued" || states["missing"] != "blocked" || states["default"] != "blocked" || states["disabled"] != "blocked" {
		t.Fatalf("states %+v", states)
	}
	host.denied["calculate"] = true
	page, err = s.MatchConversationAgents(t.Context(), request, a)
	if err != nil || page.RecommendedAgentID != "" {
		t.Fatalf("revoked capability was recommended: %+v %v", page, err)
	}
	request.ConversationID = "another-owner"
	if _, err = s.MatchConversationAgents(t.Context(), request, a); err == nil {
		t.Fatal("inaccessible source accepted")
	}
}

func TestPeerDiscoveryDoesNotSharePrivateSourcesOrTreatUnknownCostAsFree(t *testing.T) {
	s, _, _, a := discoveryFixture()
	r := sdk.ConversationAgentMatchRequest{Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{{ConversationID: "private-source", RunID: "source-run"}}}}
	page, err := s.MatchConversationAgents(t.Context(), r, a)
	if err != nil || page.RecommendedAgentID != "" {
		t.Fatalf("private sources matched %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.SourceAccess != "denied" {
			t.Fatal("private attachment access was implied")
		}
	}
	zero := 0.0
	r.Requirements = sdk.ConversationAgentRequirements{InputTokens: 100, OutputTokens: 20, MaxModelCost: &zero, Currency: "CNY"}
	page, err = s.MatchConversationAgents(t.Context(), r, a)
	if err != nil || page.RecommendedAgentID != "" {
		t.Fatalf("unknown price treated as free: %+v %v", page, err)
	}
	s.options.AgentModelPrices = map[string]sdk.ConversationModelPrice{"default": {Currency: "CNY", InputPerMillion: 2, OutputPerMillion: 4, Basis: "fixture tariff", UpdatedAt: time.Now().UTC()}}
	ceiling := 1.0
	r.Requirements.MaxModelCost = &ceiling
	page, err = s.MatchConversationAgents(t.Context(), r, a)
	if err != nil || page.RecommendedAgentID == "" {
		t.Fatalf("priced match %+v %v", page, err)
	}
	for _, item := range page.Items {
		if !item.Cost.Known || math.Abs(item.Cost.Amount-.00028) > 1e-10 {
			t.Fatalf("cost %+v", item.Cost)
		}
	}
}

func TestPeerDiscoveryHistoryKeepsModelRevisionAndTaskTypeSeparate(t *testing.T) {
	s, r, _, a := discoveryFixture()
	snapshot, err := s.freezeConversationAgent(t.Context(), "ready", a)
	if err != nil {
		t.Fatal(err)
	}
	base := persistence.ConversationAgentObservation{AgentID: "ready", Revision: 1, Model: snapshot.ModelIdentity, TaskType: "review", Status: "completed", DurationMillis: 120, ToolCalls: 2, Usage: map[string]any{"input_tokens": 100, "output_tokens": 30, "cache_read_input_tokens": 20}}
	base.ConfigurationDigest = snapshot.Digest
	r.observations.History = []persistence.ConversationAgentObservation{base}
	for _, kind := range []string{"revision", "model", "type", "profile"} {
		old := base
		switch kind {
		case "revision":
			old.Revision = 2
		case "model":
			old.Model.Fingerprint = "different"
		case "type":
			old.TaskType = "different"
		case "profile":
			old.ConfigurationDigest = "different"
		}
		r.observations.History = append(r.observations.History, old)
	}
	accepted := base
	accepted.Status = "accepted_delivery"
	accepted.CoordinationMillis = 900
	r.observations.History = append(r.observations.History, accepted)
	h := agentHistory(r.observations.History, snapshot, "review")
	if h.Runs != 1 || h.UsageSamples != 1 || h.MeanInputTokens != 120 || h.MeanOutputTokens != 30 || h.AcceptedDeliveries != 1 || h.CompletedRuns != 1 || h.AcceptanceRate != 1 || h.AcceptanceRateKnown || h.MeanCoordinationMillis != 900 {
		t.Fatalf("history mixed incompatible observations: %+v", h)
	}
	for _, usage := range []map[string]any{{"total_tokens": 20}, {"input_tokens": nil, "output_tokens": 2}, {"input_tokens": -1, "output_tokens": 2}, {"input_tokens": 1.5, "output_tokens": 2}} {
		if _, _, ok := usageTokenCounts(usage); ok {
			t.Fatal("invented usage", usage)
		}
	}
	cost := s.estimateAgentCost("default", sdk.ConversationAgentRequirements{}, h)
	if cost.Known || !strings.Contains(cost.Basis, "price_unconfigured") {
		t.Fatalf("invented cost %+v", cost)
	}
}

func TestPeerDiscoveryUsesReliableCoordinationTimeForSelection(t *testing.T) {
	fastCoordination := sdk.ConversationAgentCandidate{AgentID: "z-fast", State: "ready", CanAccept: true, History: sdk.ConversationAgentHistory{AcceptedDeliveries: 2, ReviewedDeliveries: 3, MeanCoordinationMillis: 100}}
	slowCoordination := sdk.ConversationAgentCandidate{AgentID: "a-slow", State: "ready", CanAccept: true, History: sdk.ConversationAgentHistory{AcceptedDeliveries: 2, ReviewedDeliveries: 3, MeanCoordinationMillis: 900}}
	if !preferAgentCandidate(fastCoordination, slowCoordination, "") || preferAgentCandidate(slowCoordination, fastCoordination, "") {
		t.Fatal("reliable reviewed coordination time did not influence selection")
	}
	fastCoordination.History.ReviewedDeliveries = 2
	slowCoordination.History.ReviewedDeliveries = 2
	if preferAgentCandidate(fastCoordination, slowCoordination, "") || !preferAgentCandidate(slowCoordination, fastCoordination, "") {
		t.Fatal("sparse coordination samples displaced the stable fallback order")
	}
}

func TestPeerDiscoveryModelDirectoryIsBoundedAndExplicitlyPartial(t *testing.T) {
	s, r, _, a := discoveryFixture()
	template := r.agents[0]
	r.agents = nil
	for i := 0; i < 64; i++ {
		item := template
		item.ID = fmt.Sprintf("agent_%02d", i)
		item.Description = strings.Repeat("a", 2048)
		r.agents = append(r.agents, item)
	}
	host := collaborationToolHost{service: s}
	var definition sdk.ConversationToolDefinition
	for _, d := range sdk.ConversationCollaborationTools() {
		if d.Key == "agent_list" {
			definition = d
		}
	}
	result, err := host.InvokeConversationTool(t.Context(), sdk.ConversationToolRequest{Authority: a, ConversationID: "source", Definition: definition, Call: sdk.ConversationToolCall{Name: "agent_list", Arguments: `{"requirements":{"tools":["calculate"]}}`}})
	if err != nil || result.Status != "completed" || len(result.Content) > 65536 {
		t.Fatalf("unbounded directory %d %v", len(result.Content), err)
	}
	var page sdk.ConversationAgentPage
	if err = json.Unmarshal(result.Content, &page); err != nil {
		t.Fatal(err)
	}
	if page.Complete || len(page.Items) != 16 || len(page.Availability) != 16 {
		t.Fatalf("incomplete directory hidden %+v", page)
	}
}

func TestPeerDiscoveryToolSchemaAcceptsRequirementsAndRejectsAuthority(t *testing.T) {
	for _, d := range sdk.ConversationCollaborationTools() {
		if d.Key != "agent_list" {
			continue
		}
		schema, err := compileConversationSchema(d.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`{}`, `{"requirements":{"tools":["calculate"],"task_type":"review"}}`} {
			var value any
			json.Unmarshal([]byte(raw), &value)
			if err = schema.Validate(value); err != nil {
				t.Fatal(err)
			}
		}
		var forged any
		json.Unmarshal([]byte(`{"authority":{"user_id":"other"}}`), &forged)
		if schema.Validate(forged) == nil {
			t.Fatal("tool accepted authority")
		}
	}
}
