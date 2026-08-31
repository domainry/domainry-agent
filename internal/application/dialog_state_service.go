package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type DialogStateService struct {
	repository agentpersistence.AgentStateRepository
	now        func() time.Time
}

func NewDialogStateService(repository agentpersistence.AgentStateRepository) *DialogStateService {
	return &DialogStateService{repository: repository, now: time.Now}
}

func (s *DialogStateService) ListSessions(ctx context.Context, query agentsdk.AgentSessionQuery, authority agentsdk.AgentAuthority) ([]agentsdk.AgentSession, error) {
	if err := validateAuthority(authority); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil {
		return nil, unavailable("agent.dialog_state.unavailable")
	}
	states, err := s.repository.List(ctx, strings.TrimSpace(authority.WorkspaceID), "session", strings.TrimSpace(authority.UserID), strings.TrimSpace(authority.RoleKey))
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	out := make([]agentsdk.AgentSession, 0, len(states))
	for _, state := range states {
		var record agentsdk.AgentSession
		if json.Unmarshal(state.Payload, &record) != nil || !sessionVisible(record, authority) || record.Archived && !query.IncludeArchived {
			continue
		}
		if query.ObjectKey != "" && record.ObjectKey != query.ObjectKey || query.RecordID != "" && record.RecordID != query.RecordID || search != "" && !sessionMatches(record, search) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *DialogStateService) UpsertSession(ctx context.Context, request agentsdk.AgentSessionUpsertRequest, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	if err := validateAuthority(authority); err != nil {
		return agentsdk.AgentSession{}, err
	}
	if s == nil || s.repository == nil {
		return agentsdk.AgentSession{}, unavailable("agent.dialog_state.unavailable")
	}
	now := s.now().UTC().UnixNano()
	externalID := strings.TrimSpace(request.ExternalSessionID)
	if externalID == "" {
		externalID = "session:" + statePart(authority.UserID) + ":" + strconv.FormatInt(now, 36)
	}
	record := agentsdk.AgentSession{ExternalSessionID: externalID, AgentSessionID: strings.TrimSpace(request.AgentSessionID), Title: sessionTitle(request.Title, request.LastSummary), LastSummary: sessionSummary(request.LastSummary), Mode: strings.TrimSpace(request.Mode), WorkspaceID: strings.TrimSpace(authority.WorkspaceID), UserID: strings.TrimSpace(authority.UserID), Role: strings.TrimSpace(authority.RoleKey), Archived: request.Archived, Context: cloneMap(request.Context), CreatedAt: now, UpdatedAt: now}
	record.ObjectKey, record.RecordID = contextString(record.Context, "object_key"), contextString(record.Context, "record_id")
	key := stateKey(record.WorkspaceID, record.UserID, record.Role, externalID)
	previousState, exists, err := s.repository.Get(ctx, record.WorkspaceID, "session", key)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	if exists {
		var previous agentsdk.AgentSession
		if json.Unmarshal(previousState.Payload, &previous) != nil || !sessionVisible(previous, authority) {
			return agentsdk.AgentSession{}, notFound("agent_dialog.session_not_found")
		}
		record.CreatedAt = previous.CreatedAt
		if record.AgentSessionID == "" {
			record.AgentSessionID = previous.AgentSessionID
		}
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	value := agentmodel.AgentStateRecord{Kind: "session", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt}
	if !exists {
		if err := s.repository.Put(ctx, record.WorkspaceID, value); err != nil {
			return agentsdk.AgentSession{}, err
		}
		return record, nil
	}
	updated, err := s.repository.CompareAndSwap(ctx, record.WorkspaceID, value, previousState.UpdatedAt)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	if !updated {
		return agentsdk.AgentSession{}, conflict("agent_dialog.session_update_conflict")
	}
	return record, nil
}

func (s *DialogStateService) SetSessionArchived(ctx context.Context, externalID string, archived bool, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	if err := validateAuthority(authority); err != nil {
		return agentsdk.AgentSession{}, err
	}
	if s == nil || s.repository == nil {
		return agentsdk.AgentSession{}, unavailable("agent.dialog_state.unavailable")
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return agentsdk.AgentSession{}, badRequest("agent_dialog.session_id_required")
	}
	workspaceID := strings.TrimSpace(authority.WorkspaceID)
	key := stateKey(workspaceID, authority.UserID, authority.RoleKey, externalID)
	state, ok, err := s.repository.Get(ctx, workspaceID, "session", key)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	if !ok {
		return agentsdk.AgentSession{}, notFound("agent_dialog.session_not_found")
	}
	var record agentsdk.AgentSession
	if json.Unmarshal(state.Payload, &record) != nil || !sessionVisible(record, authority) {
		return agentsdk.AgentSession{}, notFound("agent_dialog.session_not_found")
	}
	record.Archived, record.UpdatedAt = archived, s.now().UTC().UnixNano()
	payload, err := json.Marshal(record)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	updated, err := s.repository.CompareAndSwap(ctx, workspaceID, agentmodel.AgentStateRecord{Kind: "session", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt}, state.UpdatedAt)
	if err != nil {
		return agentsdk.AgentSession{}, err
	}
	if !updated {
		return agentsdk.AgentSession{}, conflict("agent_dialog.session_update_conflict")
	}
	return record, nil
}

func (s *DialogStateService) ListProposals(ctx context.Context, status string, authority agentsdk.AgentAuthority) ([]agentsdk.AgentProposal, error) {
	if err := validateAuthority(authority); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil {
		return nil, unavailable("agent.dialog_state.unavailable")
	}
	states, err := s.repository.List(ctx, strings.TrimSpace(authority.WorkspaceID), "proposal", strings.TrimSpace(authority.UserID), strings.TrimSpace(authority.RoleKey))
	if err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	out := make([]agentsdk.AgentProposal, 0, len(states))
	for _, state := range states {
		var value agentsdk.AgentProposal
		if json.Unmarshal(state.Payload, &value) != nil || !proposalVisible(value, authority) || status != "" && value.Status != status {
			continue
		}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (s *DialogStateService) GetProposal(ctx context.Context, proposalID string, authority agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	if err := validateAuthority(authority); err != nil {
		return agentsdk.AgentProposal{}, err
	}
	if s == nil || s.repository == nil {
		return agentsdk.AgentProposal{}, unavailable("agent.dialog_state.unavailable")
	}
	workspaceID := strings.TrimSpace(authority.WorkspaceID)
	state, ok, err := s.repository.Get(ctx, workspaceID, "proposal", stateKey(workspaceID, authority.UserID, authority.RoleKey, statePart(proposalID)))
	if err != nil {
		return agentsdk.AgentProposal{}, err
	}
	if !ok {
		return agentsdk.AgentProposal{}, notFound("agent_dialog.proposal_not_found")
	}
	var value agentsdk.AgentProposal
	if json.Unmarshal(state.Payload, &value) != nil || !proposalVisible(value, authority) {
		return agentsdk.AgentProposal{}, notFound("agent_dialog.proposal_not_found")
	}
	return value, nil
}

func (s *DialogStateService) StoreProposal(ctx context.Context, record agentsdk.AgentProposal) (agentsdk.AgentProposal, error) {
	workspaceID := strings.TrimSpace(record.WorkspaceID)
	if workspaceID == "" {
		return agentsdk.AgentProposal{}, forbidden("backend.workspace_scope_required")
	}
	if s == nil || s.repository == nil {
		return agentsdk.AgentProposal{}, unavailable("agent.dialog_state.unavailable")
	}
	if strings.TrimSpace(record.ProposalID) == "" {
		return agentsdk.AgentProposal{}, badRequest("agent_dialog.proposal_id_required")
	}
	if record.Status == "" {
		record.Status = "draft"
	}
	now := s.now().UTC().UnixNano()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt, record.Audited = now, true
	payload, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	key := stateKey(workspaceID, record.UserID, record.Role, statePart(record.ProposalID))
	err = s.repository.Put(ctx, workspaceID, agentmodel.AgentStateRecord{Kind: "proposal", Key: key, WorkspaceID: workspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt})
	return record, err
}

func (s *DialogStateService) DecideProposal(ctx context.Context, decision agentsdk.AgentProposalDecision, authority agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	value, err := s.GetProposal(ctx, decision.ProposalID, authority)
	if err != nil {
		return agentsdk.AgentProposal{}, err
	}
	expectedUpdatedAt := value.UpdatedAt
	if decision.ExpectedUpdatedAt != 0 {
		if decision.ExpectedUpdatedAt != value.UpdatedAt {
			return value, conflict("agent_dialog.proposal_decision_conflict")
		}
		expectedUpdatedAt = decision.ExpectedUpdatedAt
	}
	now := s.now().UTC().UnixNano()
	value.Status, value.DecisionActor, value.DecisionReason, value.DecidedAt, value.UpdatedAt, value.Audited = strings.TrimSpace(decision.Decision), strings.TrimSpace(authority.UserID), strings.TrimSpace(decision.Reason), now, now, true
	if decision.Metadata != nil {
		value.Metadata = cloneMap(decision.Metadata)
	}
	if decision.Execution != nil {
		value.Execution = cloneMap(decision.Execution)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	key := stateKey(value.WorkspaceID, value.UserID, value.Role, statePart(value.ProposalID))
	updated, err := s.repository.CompareAndSwap(ctx, value.WorkspaceID, agentmodel.AgentStateRecord{Kind: "proposal", Key: key, WorkspaceID: value.WorkspaceID, UserID: value.UserID, RoleKey: value.Role, Payload: payload, UpdatedAt: value.UpdatedAt}, expectedUpdatedAt)
	if err != nil {
		return value, err
	}
	if !updated {
		return value, conflict("agent_dialog.proposal_decision_conflict")
	}
	return value, nil
}

func validateAuthority(authority agentsdk.AgentAuthority) error {
	if strings.TrimSpace(authority.WorkspaceID) == "" {
		return forbidden("backend.workspace_scope_required")
	}
	return nil
}

func statePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "anonymous"
	}
	return strings.ReplaceAll(value, ":", "_")
}

func stateKey(workspaceID, userID, role, id string) string {
	return statePart(workspaceID) + ":" + statePart(userID) + ":" + statePart(role) + ":" + id
}

func sessionVisible(record agentsdk.AgentSession, authority agentsdk.AgentAuthority) bool {
	return statePart(record.WorkspaceID) == statePart(authority.WorkspaceID) && statePart(record.UserID) == statePart(authority.UserID) && statePart(record.Role) == statePart(authority.RoleKey)
}

func proposalVisible(record agentsdk.AgentProposal, authority agentsdk.AgentAuthority) bool {
	return strings.TrimSpace(record.WorkspaceID) == strings.TrimSpace(authority.WorkspaceID) && strings.TrimSpace(record.UserID) == strings.TrimSpace(authority.UserID) && strings.TrimSpace(record.Role) == strings.TrimSpace(authority.RoleKey)
}

func sessionMatches(record agentsdk.AgentSession, search string) bool {
	return strings.Contains(strings.ToLower(record.Title+" "+record.LastSummary+" "+record.ExternalSessionID+" "+record.AgentSessionID), search)
}

func sessionTitle(title, summary string) string {
	if value := strings.TrimSpace(title); value != "" {
		return truncate(value, 120)
	}
	if value := strings.TrimSpace(summary); value != "" {
		return truncate(value, 120)
	}
	return "New conversation"
}

func sessionSummary(value string) string { return truncate(strings.TrimSpace(value), 500) }

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func contextString(value map[string]any, key string) string {
	text := strings.TrimSpace(fmt.Sprint(value[key]))
	if text == "<nil>" {
		return ""
	}
	return text
}

func sdkError(class, code string) error { return &agentsdk.Error{Class: class, Code: code} }
func badRequest(code string) error      { return sdkError("bad_request", code) }
func forbidden(code string) error       { return sdkError("forbidden", code) }
func notFound(code string) error        { return sdkError("not_found", code) }
func conflict(code string) error        { return sdkError("conflict", code) }
func unavailable(code string) error     { return sdkError("unavailable", code) }

var _ agentsdk.AgentDialogStateService = (*DialogStateService)(nil)
