package server

import (
	"errors"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Query     agentsdk.AgentSessionQuery `json:"query"`
		Authority agentsdk.AgentAuthority    `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.ListSessions(r.Context(), request.Query, request.Authority)
	writeDialogStateResult(w, value, err)
}

func (s *Server) upsertSession(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Input     agentsdk.AgentSessionUpsertRequest `json:"input"`
		Authority agentsdk.AgentAuthority            `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.UpsertSession(r.Context(), request.Input, request.Authority)
	writeDialogStateResult(w, value, err)
}

func (s *Server) setSessionArchived(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Archived  bool                    `json:"archived"`
		Authority agentsdk.AgentAuthority `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.SetSessionArchived(r.Context(), r.PathValue("id"), request.Archived, request.Authority)
	writeDialogStateResult(w, value, err)
}

func (s *Server) listProposals(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Status    string                  `json:"status,omitempty"`
		Authority agentsdk.AgentAuthority `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.ListProposals(r.Context(), request.Status, request.Authority)
	writeDialogStateResult(w, value, err)
}

func (s *Server) getProposal(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Authority agentsdk.AgentAuthority `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.GetProposal(r.Context(), r.PathValue("id"), request.Authority)
	writeDialogStateResult(w, value, err)
}

func (s *Server) storeProposal(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request agentsdk.AgentProposal
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.StoreProposal(r.Context(), request)
	writeDialogStateResult(w, value, err)
}

func (s *Server) decideProposal(w http.ResponseWriter, r *http.Request) {
	if s.config.DialogState == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.dialog_state.unavailable", "dialog state unavailable")
		return
	}
	var request struct {
		Decision  agentsdk.AgentProposalDecision `json:"decision"`
		Authority agentsdk.AgentAuthority        `json:"authority"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	value, err := s.config.DialogState.DecideProposal(r.Context(), request.Decision, request.Authority)
	writeDialogStateResult(w, value, err)
}

func writeDialogStateResult(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	status, code := http.StatusInternalServerError, "agent.dialog_state.failed"
	var sdkError *agentsdk.Error
	if errors.As(err, &sdkError) {
		code = sdkError.ErrorCode()
		switch sdkError.Class {
		case "bad_request":
			status = http.StatusBadRequest
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
	}
	writeError(w, status, code, err.Error())
}
