package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func validateAgentModelPrice(key string, price agentsdk.ConversationModelPrice, models map[string]agentsdk.ConversationModel) error {
	if key != "default" && models[key] == nil || !conversationText(price.Currency, 16, true) || !conversationText(price.Basis, 512, true) || price.UpdatedAt.IsZero() || price.UpdatedAt.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("invalid Agent model price configuration for %s", key)
	}
	for _, value := range []float64{price.InputPerMillion, price.OutputPerMillion} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1e9 {
			return fmt.Errorf("invalid Agent model price rate for %s", key)
		}
	}
	return nil
}

func validateAgentRequirements(r agentsdk.ConversationAgentRequirements) error {
	if r.TaskType != "" && !conversationKey(r.TaskType) || len(r.Tools) > 32 || len(r.Skills) > 32 || len(r.Sources) > 16 || r.InputTokens < 0 || r.OutputTokens < 0 || r.InputTokens > 1_000_000_000 || r.OutputTokens > 1_000_000_000 || (r.InputTokens == 0) != (r.OutputTokens == 0) || len(r.Currency) > 16 {
		return conversationFailure("bad_request", "agent_requirements_invalid")
	}
	if r.MaxModelCost != nil && (math.IsNaN(*r.MaxModelCost) || math.IsInf(*r.MaxModelCost, 0) || *r.MaxModelCost < 0 || r.Currency == "") {
		return conversationFailure("bad_request", "agent_requirements_invalid")
	}
	for _, group := range [][]string{r.Tools, r.Skills} {
		seen := map[string]bool{}
		for _, key := range group {
			if !conversationKey(key) || seen[key] {
				return conversationFailure("bad_request", "agent_requirements_invalid")
			}
			seen[key] = true
		}
	}
	for _, ref := range r.Sources {
		if !conversationKey(ref.ConversationID) || !conversationKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
			return conversationFailure("bad_request", "agent_requirements_invalid")
		}
	}
	return nil
}

func (s *ConversationService) MatchConversationAgents(ctx context.Context, in agentsdk.ConversationAgentMatchRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMatchPage, error) {
	var out agentsdk.ConversationAgentMatchPage
	if err := s.authorize(a); err != nil {
		return out, err
	}
	if err := validateAgentRequirements(in.Requirements); err != nil {
		return out, err
	}
	if in.ConversationID != "" && !conversationKey(in.ConversationID) {
		return out, conversationFailure("bad_request", "agent_requirements_invalid")
	}
	page, err := s.conversationAgentDirectory(ctx, a)
	if err != nil {
		return out, err
	}
	return s.matchConversationAgentDirectory(ctx, page, in, a)
}

func (s *ConversationService) matchConversationAgentDirectory(ctx context.Context, page agentsdk.ConversationAgentPage, in agentsdk.ConversationAgentMatchRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMatchPage, error) {
	out := agentsdk.ConversationAgentMatchPage{Items: []agentsdk.ConversationAgentCandidate{}, CheckedAt: time.Now().UTC(), HistoryComplete: true, Basis: "Current owner authorization, registered capabilities, durable queue/leases and the latest 200 terminal owner runs. Execution admission checks these facts again; a recommendation is not a reservation. Ranking: readiness, reviewed outcomes (at least 3 samples), backlog, comparable model cost, duration, stable ID."}
	repo, ok := s.repo.(persistence.ConversationAgentDiscoveryRepository)
	if !ok {
		return out, conversationFailure("unavailable", "agent_discovery_unavailable")
	}
	observations, err := repo.ConversationAgentObservations(ctx, a)
	if err != nil {
		return out, err
	}
	out.HistoryComplete = observations.HistoryComplete
	var capacity agentsdk.ConversationExecutionCapacity
	if cr, ok := s.repo.(persistence.ConversationCapacityRepository); ok {
		capacity, err = cr.ConversationExecutionCapacity(ctx, a)
		if err != nil {
			return out, err
		}
	}
	excluded, err := s.delegationAncestors(ctx, in.ConversationID, a)
	if err != nil {
		return out, err
	}
	// The directory has already intersected deployment selection, Identity and
	// connection/settings availability. Also check the underlying tool grant.
	usable := map[string]bool{}
	for _, tool := range page.Tools {
		auth, err := s.options.ToolHost.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: tool})
		if err != nil {
			return out, err
		}
		usable[tool.Key] = auth.Granted
	}
	// A new isolated conversation cannot inherit any private attachment scope.
	// The prospective consumer is a server-only sentinel, never a user ID.
	sourceAccess := "not_requested"
	if len(in.Requirements.Sources) > 0 {
		sourceAccess = "verified"
		audit := s.sourceAudit(a, "new_peer_conversation")
		for _, ref := range in.Requirements.Sources {
			if _, err := audit.run(ctx, ref); err != nil {
				sourceAccess = "denied"
				break
			}
		}
	}
	for _, agent := range page.Items {
		load := observations.Load[agent.ID]
		candidate := agentsdk.ConversationAgentCandidate{AgentID: agent.ID, Revision: agent.Revision, State: "ready", CanAccept: true, Running: load.Running, Queued: load.Queued, Waiting: load.Waiting, AvailableSlots: max(0, agent.MaxConcurrent-load.Running), SourceAccess: sourceAccess, Reasons: []string{}, MissingTools: []string{}, MissingSkills: []string{}, UnavailableTools: []string{}, CheckedAt: out.CheckedAt}
		block := func(reason string) {
			candidate.CanAccept = false
			candidate.State = "blocked"
			candidate.Reasons = append(candidate.Reasons, reason)
		}
		if err := s.authorizeCollaboration(ctx, "receive", &agentsdk.ConversationDelegation{ToAgentID: agent.ID}, a); err != nil {
			if collaborationDenied(err) {
				block("receiver_access_denied")
			} else {
				return out, err
			}
		}
		if !agent.Enabled {
			block("agent_disabled")
		}
		snapshot, freezeErr := s.freezeConversationAgent(ctx, agent.ID, a)
		if freezeErr != nil && agent.Enabled {
			block("agent_model_or_profile_unavailable")
		}
		if excluded[agent.ID] {
			block("delegation_cycle")
		}
		if sourceAccess == "denied" {
			block("source_access_denied")
		}
		tools, skills := map[string]bool{}, map[string]bool{}
		for _, key := range agent.Tools {
			if strings.HasPrefix(key, "task_") {
				continue
			}
			tools[key] = true
			if !usable[key] {
				candidate.UnavailableTools = append(candidate.UnavailableTools, key)
			}
		}
		for _, key := range agent.SkillKeys {
			skills[key] = true
		}
		for _, key := range in.Requirements.Tools {
			if !tools[key] {
				candidate.MissingTools = append(candidate.MissingTools, key)
			}
		}
		for _, key := range in.Requirements.Skills {
			if !skills[key] {
				candidate.MissingSkills = append(candidate.MissingSkills, key)
			}
		}
		if len(candidate.UnavailableTools) > 0 {
			block("configured_tools_unavailable")
		}
		if len(candidate.MissingTools) > 0 || len(candidate.MissingSkills) > 0 {
			block("capabilities_missing")
		}
		if s.options.MaxQueuedPerUser > 0 && capacity.UserQueued >= s.options.MaxQueuedPerUser || s.options.MaxQueuedPerWorkspace > 0 && capacity.WorkspaceQueued >= s.options.MaxQueuedPerWorkspace {
			block("queue_full")
		}
		if candidate.AvailableSlots == 0 || candidate.Queued > 0 || s.options.MaxRunningPerUser > 0 && capacity.UserRunning >= s.options.MaxRunningPerUser || s.options.MaxRunningPerWorkspace > 0 && capacity.WorkspaceRunning >= s.options.MaxRunningPerWorkspace {
			candidate.AvailableSlots = 0
			if candidate.CanAccept {
				candidate.State = "queued"
				candidate.Reasons = append(candidate.Reasons, "wait_for_capacity")
			}
		}
		candidate.History = agentHistory(observations.History, snapshot, in.Requirements.TaskType)
		candidate.Cost = s.estimateAgentCost(agent.ModelKey, in.Requirements, candidate.History)
		if maxCost := in.Requirements.MaxModelCost; maxCost != nil {
			if !candidate.Cost.Known || candidate.Cost.Currency != in.Requirements.Currency {
				block("cost_unknown")
			} else if candidate.Cost.Amount > *maxCost {
				block("estimated_cost_exceeded")
			}
		}
		out.Items = append(out.Items, candidate)
	}
	// Only compare monetary values when all eligible candidates have estimates
	// in one currency. Conditional pairwise price sorting is not transitive.
	priceCurrency, comparable := "", true
	for _, item := range out.Items {
		if item.CanAccept {
			if !item.Cost.Known {
				comparable = false
			}
			if priceCurrency == "" {
				priceCurrency = item.Cost.Currency
			} else if priceCurrency != item.Cost.Currency {
				comparable = false
			}
		}
	}
	if !comparable {
		priceCurrency = ""
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return preferAgentCandidate(out.Items[i], out.Items[j], priceCurrency) })
	for _, item := range out.Items {
		if item.CanAccept {
			out.RecommendedAgentID = item.AgentID
			break
		}
	}
	return out, nil
}

func (s *ConversationService) delegationAncestors(ctx context.Context, id string, a agentsdk.ConversationAuthority) (map[string]bool, error) {
	seen := map[string]bool{}
	if id == "" {
		return seen, nil
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return nil, err
	}
	for depth := 0; depth < 9; depth++ {
		c, err := s.repo.Get(ctx, id, a)
		if err != nil {
			return nil, err
		}
		if depth == 0 && c.Archived {
			return nil, conversationFailure("conflict", "archived")
		}
		agentID := c.AgentID
		if agentID == "" {
			agentID = "default"
		}
		seen[agentID] = true
		if c.DelegationID == "" {
			return seen, nil
		}
		d, err := repo.ConversationDelegation(ctx, c.DelegationID, a)
		if err != nil {
			return nil, err
		}
		seen[d.ToAgentID] = true
		id = d.SourceConversationID
	}
	return nil, conversationFailure("conflict", "delegation_depth_exceeded")
}

func agentHistory(items []persistence.ConversationAgentObservation, snapshot *agentsdk.ConversationAgentSnapshot, taskType string) agentsdk.ConversationAgentHistory {
	out := agentsdk.ConversationAgentHistory{Basis: "Same Agent configuration revision and model identity; matching task type when specified. Completed execution is not accepted delivery. Durations exclude queue time. Usage samples cover individual terminal runs, not a full goal."}
	if snapshot == nil {
		return out
	}
	var calls int
	for _, item := range items {
		if item.AgentID != snapshot.ID || item.Revision != snapshot.Revision || item.ConfigurationDigest != snapshot.Digest || item.Model != snapshot.ModelIdentity || taskType != "" && item.TaskType != taskType {
			continue
		}
		switch item.Status {
		case "accepted_delivery":
			out.AcceptedDeliveries++
			out.ReviewedDeliveries++
			continue
		case "needs_changes":
			out.ReviewedDeliveries++
			continue
		case "completed":
			out.CompletedRuns++
		case "failed":
			out.FailedRuns++
		}
		out.Runs++
		out.MeanDurationMillis += item.DurationMillis
		calls += item.ToolCalls
		input, output, ok := usageTokenCounts(item.Usage)
		if ok {
			out.UsageSamples++
			out.MeanInputTokens += input
			out.MeanOutputTokens += output
		}
	}
	if out.Runs > 0 {
		out.MeanDurationMillis /= int64(out.Runs)
		out.MeanToolCalls = float64(calls) / float64(out.Runs)
	}
	if out.UsageSamples > 0 {
		out.MeanInputTokens /= int64(out.UsageSamples)
		out.MeanOutputTokens /= int64(out.UsageSamples)
	}
	return out
}

func usageTokenCounts(usage map[string]any) (int64, int64, bool) {
	count := func(keys ...string) (int64, bool) {
		for _, key := range keys {
			if value, ok := usage[key]; ok {
				if value == nil {
					return 0, false
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return 0, false
				}
				var n float64
				if json.Unmarshal(raw, &n) != nil || n < 0 || n > 1e12 || math.Trunc(n) != n {
					return 0, false
				}
				return int64(n), true
			}
		}
		return 0, false
	}
	input, iok := count("input_tokens", "prompt_tokens")
	output, ook := count("output_tokens", "completion_tokens")
	// Anthropic reports cache input tokens separately; charge at uncached price
	// as a conservative estimate because cached-price contracts vary by model.
	for _, key := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if _, present := usage[key]; present {
			n, ok := count(key)
			if !ok {
				return 0, 0, false
			}
			input += n
		}
	}
	return input, output, iok && ook
}

func (s *ConversationService) estimateAgentCost(key string, r agentsdk.ConversationAgentRequirements, history agentsdk.ConversationAgentHistory) agentsdk.ConversationAgentCostEstimate {
	out := agentsdk.ConversationAgentCostEstimate{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, Basis: "usage_unknown", Uncertainty: "Model charges only; tool charges, retries, future revisions and cross-Agent work are excluded. This is an estimate, not a spend limit."}
	if out.InputTokens > 0 {
		out.Basis = "requested_total_token_estimate"
	} else if history.UsageSamples > 0 {
		out.InputTokens, out.OutputTokens = history.MeanInputTokens, history.MeanOutputTokens
		out.Basis = "historical_mean_per_run"
	}
	price, exists := s.options.AgentModelPrices[key]
	if !exists {
		out.Basis += "; price_unconfigured"
		return out
	}
	out.Price, out.Currency = &price, price.Currency
	if out.Basis == "usage_unknown" {
		return out
	}
	out.Known = true
	out.Amount = (float64(out.InputTokens)*price.InputPerMillion + float64(out.OutputTokens)*price.OutputPerMillion) / 1e6
	return out
}

func preferAgentCandidate(a, b agentsdk.ConversationAgentCandidate, currency string) bool {
	rank := func(s string) int {
		switch s {
		case "ready":
			return 0
		case "queued":
			return 1
		}
		return 2
	}
	if rank(a.State) != rank(b.State) {
		return rank(a.State) < rank(b.State)
	}
	quality := func(h agentsdk.ConversationAgentHistory) float64 {
		if h.ReviewedDeliveries < 3 {
			return .5
		}
		return float64(h.AcceptedDeliveries+1) / float64(h.ReviewedDeliveries+2)
	}
	if quality(a.History) != quality(b.History) {
		return quality(a.History) > quality(b.History)
	}
	if a.Queued != b.Queued {
		return a.Queued < b.Queued
	}
	cost := func(c agentsdk.ConversationAgentCandidate) float64 {
		if c.Cost.Known && c.Cost.Currency == currency {
			return c.Cost.Amount
		}
		return math.MaxFloat64
	}
	if currency != "" && cost(a) != cost(b) {
		return cost(a) < cost(b)
	}
	duration := func(c agentsdk.ConversationAgentCandidate) int64 {
		if c.History.Runs >= 3 {
			return c.History.MeanDurationMillis
		}
		return math.MaxInt64
	}
	if duration(a) != duration(b) {
		return duration(a) < duration(b)
	}
	return a.AgentID < b.AgentID
}
