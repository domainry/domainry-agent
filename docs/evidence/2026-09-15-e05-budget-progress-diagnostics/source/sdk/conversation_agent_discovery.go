package agentsdk

import "time"

// A reusable capability definition is not a running Agent. Several independent
// instances can use one definition without sharing identity, queues or context.
type ConversationAgentDefinition struct {
	Key          string   `json:"key"`
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	Tools        []string `json:"tools"`
	SkillKeys    []string `json:"skill_keys"`
}

type ConversationSkillSummary struct {
	Key          string   `json:"key"`
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools"`
}

// Requirements describe task fit, never permissions. Sources are explicit run
// prefixes checked for a new isolated conversation. Admitted roots can be read
// with delegation_source_read under live source-owned result permissions;
// references never grant raw execution or access to private files.
type ConversationAgentRequirements struct {
	TaskType string                     `json:"task_type,omitempty"`
	Tools    []string                   `json:"tools,omitempty"`
	Skills   []string                   `json:"skills,omitempty"`
	Sources  []ConversationRunReference `json:"sources,omitempty"`
	// Token estimates cover the entire delegated execution, including follow-up
	// model calls. Zero means unknown, not free execution.
	InputTokens  int64    `json:"input_tokens,omitempty"`
	OutputTokens int64    `json:"output_tokens,omitempty"`
	MaxModelCost *float64 `json:"max_model_cost,omitempty"`
	Currency     string   `json:"currency,omitempty"`
}

type ConversationAgentMatchRequest struct {
	ConversationID string                        `json:"conversation_id,omitempty"`
	Requirements   ConversationAgentRequirements `json:"requirements,omitempty"`
}

// Prices are supplied only by trusted host configuration. Estimates do not
// include external tool charges and are never a hard execution spending cap.
type ConversationModelPrice struct {
	Currency         string    `json:"currency"`
	InputPerMillion  float64   `json:"input_per_million"`
	OutputPerMillion float64   `json:"output_per_million"`
	Basis            string    `json:"basis"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ConversationAgentCostEstimate struct {
	Known        bool                    `json:"known"`
	Amount       float64                 `json:"amount"`
	Currency     string                  `json:"currency,omitempty"`
	InputTokens  int64                   `json:"input_tokens"`
	OutputTokens int64                   `json:"output_tokens"`
	Basis        string                  `json:"basis"`
	Uncertainty  string                  `json:"uncertainty"`
	Price        *ConversationModelPrice `json:"price,omitempty"`
}

type ConversationAgentHistory struct {
	Runs                   int     `json:"runs"`
	CompletedRuns          int     `json:"completed_runs"`
	FailedRuns             int     `json:"failed_runs"`
	AcceptedDeliveries     int     `json:"accepted_deliveries"`
	ReviewedDeliveries     int     `json:"reviewed_deliveries"`
	AcceptanceRateKnown    bool    `json:"acceptance_rate_known"`
	AcceptanceRate         float64 `json:"acceptance_rate"`
	UsageSamples           int     `json:"usage_samples"`
	MeanInputTokens        int64   `json:"mean_input_tokens"`
	MeanOutputTokens       int64   `json:"mean_output_tokens"`
	MeanDurationMillis     int64   `json:"mean_duration_ms"`
	MeanCoordinationMillis int64   `json:"mean_coordination_ms"`
	MeanToolCalls          float64 `json:"mean_tool_calls"`
	Basis                  string  `json:"basis"`
}

type ConversationAgentCandidate struct {
	AgentID          string                        `json:"agent_id"`
	Revision         int64                         `json:"revision"`
	State            string                        `json:"state"` // ready, queued, blocked
	CanAccept        bool                          `json:"can_accept"`
	Running          int                           `json:"running"`
	Queued           int                           `json:"queued"`
	Waiting          int                           `json:"waiting"`
	AvailableSlots   int                           `json:"available_slots"`
	MissingTools     []string                      `json:"missing_tools"`
	MissingSkills    []string                      `json:"missing_skills"`
	UnavailableTools []string                      `json:"unavailable_tools"`
	SourceAccess     string                        `json:"source_access"` // not_requested, verified, denied
	Reasons          []string                      `json:"reasons"`
	Cost             ConversationAgentCostEstimate `json:"cost"`
	History          ConversationAgentHistory      `json:"history"`
	CheckedAt        time.Time                     `json:"checked_at"`
}

type ConversationAgentMatchPage struct {
	Items              []ConversationAgentCandidate `json:"items"`
	RecommendedAgentID string                       `json:"recommended_agent_id,omitempty"`
	CheckedAt          time.Time                    `json:"checked_at"`
	Basis              string                       `json:"basis"`
	HistoryComplete    bool                         `json:"history_complete"`
}
