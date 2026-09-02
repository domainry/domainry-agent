package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentcapability "github.com/domainry/domainry-agent/capability"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
)

const maxRequestBytes = 2 << 20

type Config struct {
	APIKey       string
	Runner       agentsdk.TaskRunner
	Interactive  agentsdk.InteractiveRunner
	DialogState  agentsdk.AgentDialogStateService
	Repositories agentpersistence.Binding
	Lifecycle    agentpersistence.AgentLifecycleRepository
}
type Server struct {
	config  Config
	handler http.Handler
}

func New(config Config) (*Server, error) {
	s := &Server{config: config}
	mux := http.NewServeMux()
	capabilityBinding, err := agentcapability.Open(agentcapability.Inputs{})
	if err != nil {
		return nil, fmt.Errorf("build Agent capability binding: %w", err)
	}
	capabilityHandler, err := modulecapability.NewHTTPHandler(capabilityBinding, func(*http.Request) error { return nil })
	if err != nil {
		return nil, err
	}
	handlers := map[string]http.Handler{
		actionAgentSaaSDescriptorRead: http.HandlerFunc(s.descriptor), actionAgentSaaSReadinessRead: http.HandlerFunc(s.ready),
		actionAgentSaaSTaskProviderStart: http.HandlerFunc(s.start), actionAgentSaaSTaskProviderPoll: http.HandlerFunc(s.poll), actionAgentSaaSTaskProviderCancel: http.HandlerFunc(s.cancel),
		actionAgentSaaSInteractiveRun: http.HandlerFunc(s.interactive),
		actionAgentSaaSSessionsQuery:  http.HandlerFunc(s.listSessions), actionAgentSaaSSessionsUpsert: http.HandlerFunc(s.upsertSession), actionAgentSaaSSessionsSetArchived: http.HandlerFunc(s.setSessionArchived),
		actionAgentSaaSProposalsQuery: http.HandlerFunc(s.listProposals), actionAgentSaaSProposalsGet: http.HandlerFunc(s.getProposal), actionAgentSaaSProposalsStore: http.HandlerFunc(s.storeProposal), actionAgentSaaSProposalsDecide: http.HandlerFunc(s.decideProposal),
		actionAgentSaaSCapabilitySummary: capabilityHandler, actionAgentSaaSCapabilityCategory: capabilityHandler, actionAgentSaaSCapabilityValidation: capabilityHandler,
	}
	actions, err := SaaSAuthorizationActions()
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		handler, found := handlers[action.Key]
		if strings.HasPrefix(action.Key, agentSaaSRepositoryActionPrefix) {
			operation := strings.TrimPrefix(action.Key, agentSaaSRepositoryActionPrefix)
			handler, found = http.HandlerFunc(s.repositoryHandler(operation)), operation != ""
		}
		if !found || handler == nil {
			return nil, fmt.Errorf("Agent SaaS Action %q has no handler", action.Key)
		}
		route, err := modulehttp.RouteFromAction(action)
		if err != nil {
			return nil, fmt.Errorf("project Agent SaaS Action %q: %w", action.Key, err)
		}
		mux.Handle(route.Pattern(), s.authorizeServiceAction(action.Key, handler))
		delete(handlers, action.Key)
	}
	if len(handlers) != 0 {
		return nil, fmt.Errorf("Agent SaaS handlers have no source Action")
	}
	s.handler = mux
	return s, nil
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	definitions, hasDefinitions := s.config.Repositories.(agentpersistence.DefinitionBinding)
	if s.config.Runner == nil || s.config.Interactive == nil || s.config.DialogState == nil || s.config.Repositories == nil || s.config.Repositories.AgentStateRepository() == nil || !agentExecutionRepositoriesReady(s.config.Repositories.AgentTaskRunRepository()) || !hasDefinitions || definitions.DefinitionRepository() == nil || s.config.Lifecycle == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.not_ready", "required Agent capability unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func agentExecutionRepositoriesReady(repository agentpersistence.AgentTaskRunRepository) bool {
	if repository == nil {
		return false
	}
	_, systemWorker := repository.(agentpersistence.AgentTaskRunSystemWorkerRepository)
	_, directClaim := repository.(agentpersistence.AgentTaskRunDirectClaimRepository)
	_, interactive := repository.(agentpersistence.AgentInteractiveRunRepository)
	_, toolLedger := repository.(agentpersistence.AgentToolCallLedger)
	return systemWorker && directClaim && interactive && toolLedger
}
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) authorizeServiceAction(actionKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(s.config.APIKey)
		actual := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if expected == "" || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "agent.saas.unauthorized", "unauthorized")
			return
		}
		ctx := agentsdk.WithAuthorizedServiceAction(r.Context(), actionKey, agentsdk.AgentRuntimeServiceAudience)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) descriptor(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: agentsdk.DeploymentModeSaaS, Capabilities: []string{agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, "dialog.state", "execution.state", "structured_output", "usage", "tool_callback", agentsdk.CapabilityLifecycleExecute}})
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if s.config.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.runner_unavailable", "runner unavailable")
		return
	}
	var request agentsdk.TaskRequest
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header == "" || request.IdempotencyKey != header {
		writeError(w, http.StatusBadRequest, "agent.saas.idempotency_invalid", "Idempotency-Key must match request")
		return
	}
	result, err := s.config.Runner.Start(r.Context(), request)
	writeRunnerResult(w, result, err)
}
func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	if s.config.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.runner_unavailable", "runner unavailable")
		return
	}
	result, err := s.config.Runner.Poll(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	writeRunnerResult(w, result, err)
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	if s.config.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.runner_unavailable", "runner unavailable")
		return
	}
	result, err := s.config.Runner.Cancel(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	writeRunnerResult(w, result, err)
}
func (s *Server) interactive(w http.ResponseWriter, r *http.Request) {
	if s.config.Interactive == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.interactive_unavailable", "interactive runner unavailable")
		return
	}
	var request agentsdk.InteractiveRequest
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error())
		return
	}
	if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header == "" || request.IdempotencyKey != header {
		writeError(w, http.StatusBadRequest, "agent.saas.idempotency_invalid", "Idempotency-Key must match request")
		return
	}
	result, err := s.config.Interactive.Run(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent.saas.provider_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func writeRunnerResult(w http.ResponseWriter, result agentsdk.TaskResult, err error) {
	if err != nil {
		status := http.StatusBadGateway
		if result.ErrorClass == "provider_http" && !result.Retryable {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func decode(r *http.Request, out any) error {
	reader := http.MaxBytesReader(nil, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
