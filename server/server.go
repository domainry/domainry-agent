package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
)

const maxRequestBytes = 2 << 20

type Config struct {
	APIKey       string
	Runner       agentsdk.TaskRunner
	Interactive  agentsdk.InteractiveRunner
	Repositories agentrepository.Binding
}
type Server struct {
	config  Config
	handler http.Handler
}

func New(config Config) *Server {
	s := &Server{config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/descriptor", s.descriptor)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /api/v1/task-runs", s.start)
	mux.HandleFunc("GET /api/v1/task-runs/{id}", s.poll)
	mux.HandleFunc("POST /api/v1/task-runs/{id}/cancel", s.cancel)
	mux.HandleFunc("POST /api/v1/interactive-runs", s.interactive)
	mux.HandleFunc("POST /api/v1/repository/{operation}", s.repository)
	s.handler = mux
	return s
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	definitions, hasDefinitions := s.config.Repositories.(agentrepository.DefinitionBinding)
	lifecycle, hasLifecycle := s.config.Repositories.(agentrepository.LifecycleBinding)
	if s.config.Runner == nil || s.config.Interactive == nil || s.config.Repositories == nil || s.config.Repositories.AgentStateRepository() == nil || s.config.Repositories.AgentTaskRunRepository() == nil || !hasDefinitions || definitions.DefinitionRepository() == nil || !hasLifecycle || lifecycle.AgentLifecycleRepository() == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.not_ready", "runner unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(s.config.APIKey)
		actual := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if expected == "" || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "agent.saas.unauthorized", "unauthorized")
			return
		}
		s.handler.ServeHTTP(w, r)
	})
}
func (s *Server) descriptor(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: agentsdk.DeploymentModeSaaS, Capabilities: []string{"task.start", "task.poll", "task.cancel", "interactive.run", "structured_output", "usage", "tool_callback"}})
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
