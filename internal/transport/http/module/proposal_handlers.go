package module

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type createProposalRequest struct {
	Title, Summary, Source, Reference string
	Proposed, Metadata                map[string]any
}

type proposalDecisionRequest struct {
	Reason   string
	Metadata map[string]any
}

func (s *surface) createProposal(w http.ResponseWriter, r *http.Request) {
	var payload createProposalRequest
	if !decode(w, r, &payload) {
		return
	}
	principal, ok := proposalPrincipal(r)
	if !ok {
		writeCode(w, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	metadata := cloneMap(payload.Metadata)
	if code := proposalWritePolicy("create_proposal", metadata); code != "" {
		writeCode(w, http.StatusForbidden, code)
		return
	}
	proposal, err := s.proposals.Create(r.Context(), agentapplication.CreateProposalRequest{
		ProposalID: "agent_proposal_" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36),
		Title:      payload.Title, Summary: payload.Summary, Source: payload.Source, Reference: payload.Reference,
		Proposed: cloneMap(payload.Proposed), Metadata: metadata, Principal: principal,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, proposal)
}

func (s *surface) approveProposal(w http.ResponseWriter, r *http.Request) {
	s.decideProposal(w, r, "approved")
}

func (s *surface) rejectProposal(w http.ResponseWriter, r *http.Request) {
	s.decideProposal(w, r, "rejected")
}

func (s *surface) decideProposal(w http.ResponseWriter, r *http.Request, decision string) {
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))
	if proposalID == "" {
		writeCode(w, http.StatusBadRequest, "agent_dialog.proposal_id_required")
		return
	}
	var payload proposalDecisionRequest
	if r.Body != nil && r.ContentLength != 0 && !decode(w, r, &payload) {
		return
	}
	principal, ok := proposalPrincipal(r)
	if !ok {
		writeCode(w, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	metadata := cloneMap(payload.Metadata)
	action := "reject_proposal"
	if decision == "approved" {
		action = "approve_proposal"
	}
	if code := proposalWritePolicy(action, metadata); code != "" {
		writeCode(w, http.StatusForbidden, code)
		return
	}
	metadata["proposal_id"], metadata["decision"], metadata["reason"] = proposalID, decision, strings.TrimSpace(payload.Reason)
	metadata["requesting_user"], metadata["service_role"], metadata["automation_user"] = principal.UserID, "agent_service_user", "agent_automation"
	proposal, err := s.proposals.Decide(r.Context(), proposalID, decision, payload.Reason, metadata, principal)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func proposalPrincipal(r *http.Request) (modulehost.Principal, bool) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok || !identity.Principal.Known {
		return modulehost.Principal{}, false
	}
	return modulehost.Principal{
		Known: true, WorkspaceID: strings.TrimSpace(identity.Principal.WorkspaceID), UserID: strings.TrimSpace(identity.Principal.UserID),
		RoleKey: strings.TrimSpace(identity.Principal.RoleKey), AuthorizationRevision: strings.TrimSpace(identity.Principal.AuthorizationRevision),
		RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")), CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		CausationID: strings.TrimSpace(r.Header.Get("X-Causation-ID")),
	}, true
}

func proposalWritePolicy(action string, metadata map[string]any) string {
	runMode := stringValue(metadata["run_mode"])
	if runMode == "" {
		if stringValue(metadata["agent_mode"]) == "domain-flow" {
			runMode = "suggested_write"
		} else {
			runMode = "read_only"
		}
	}
	if runMode == "read_only" {
		return "agent_dialog.policy_read_only"
	}
	if runMode != "suggested_write" {
		return "agent_dialog.policy_run_mode_denied"
	}
	if action != "create_proposal" && stringValue(metadata["risk_level"]) == "critical" {
		return "agent_dialog.policy_risk_denied"
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
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
