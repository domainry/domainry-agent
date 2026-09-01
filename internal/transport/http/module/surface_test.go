package module

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	agenttestsupport "github.com/domainry/domainry-agent/testsupport"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type surfaceBindingStub struct {
	modulecapability.Binding
	state       agentsdk.AgentDialogStateService
	tasks       agentpersistence.AgentTaskStateService
	interactive agentpersistence.AgentInteractiveStateService
}

func (surfaceBindingStub) Descriptor() agentsdk.Descriptor                 { return agentsdk.Descriptor{} }
func (surfaceBindingStub) TaskRunner() agentsdk.TaskRunner                 { return nil }
func (surfaceBindingStub) InteractiveRunner() agentsdk.InteractiveRunner   { return nil }
func (surfaceBindingStub) Close(context.Context) error                     { return nil }
func (b surfaceBindingStub) DialogState() agentsdk.AgentDialogStateService { return b.state }

func (b surfaceBindingStub) AgentTaskState() agentpersistence.AgentTaskStateService {
	if b.tasks != nil {
		return b.tasks
	}
	return agenttestsupport.NewTaskState(nil, nil)
}
func (b surfaceBindingStub) AgentInteractiveState() agentpersistence.AgentInteractiveStateService {
	if b.interactive != nil {
		return b.interactive
	}
	return agenttestsupport.NewInteractiveState(nil, nil, nil)
}

type taskStateSurfaceStub struct {
	agentpersistence.AgentTaskStateService
	runs        []agentmodel.AgentTaskRun
	run         agentmodel.AgentTaskRun
	found       bool
	workspaceID string
	filter      agentpersistence.AgentTaskRunFilter
}

func (s *taskStateSurfaceStub) Get(_ context.Context, workspaceID, _ string) (agentmodel.AgentTaskRun, bool, error) {
	s.workspaceID = workspaceID
	return s.run, s.found, nil
}

func (s *taskStateSurfaceStub) List(_ context.Context, workspaceID string, filter agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	s.workspaceID, s.filter = workspaceID, filter
	return s.runs, nil
}

type interactiveStateSurfaceStub struct {
	agentpersistence.AgentInteractiveStateService
	run       agentmodel.AgentInteractiveRun
	found     bool
	authority agentpersistence.AgentInteractiveAuthority
}

func (s *interactiveStateSurfaceStub) Get(_ context.Context, _ string, authority agentpersistence.AgentInteractiveAuthority) (agentmodel.AgentInteractiveRun, bool, error) {
	s.authority = authority
	return s.run, s.found, nil
}

type dialogStateStub struct {
	authority agentsdk.AgentAuthority
	session   agentsdk.AgentSession
	archived  bool
}

func (s *dialogStateStub) ListSessions(_ context.Context, _ agentsdk.AgentSessionQuery, authority agentsdk.AgentAuthority) ([]agentsdk.AgentSession, error) {
	s.authority = authority
	return []agentsdk.AgentSession{s.session}, nil
}
func (s *dialogStateStub) UpsertSession(_ context.Context, input agentsdk.AgentSessionUpsertRequest, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	s.authority = authority
	s.session = agentsdk.AgentSession{ExternalSessionID: input.ExternalSessionID, Title: input.Title, WorkspaceID: authority.WorkspaceID, UserID: authority.UserID, Role: authority.RoleKey}
	return s.session, nil
}
func (s *dialogStateStub) SetSessionArchived(_ context.Context, _ string, archived bool, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	s.authority, s.archived = authority, archived
	s.session.Archived = archived
	return s.session, nil
}
func (*dialogStateStub) ListProposals(context.Context, string, agentsdk.AgentAuthority) ([]agentsdk.AgentProposal, error) {
	return []agentsdk.AgentProposal{{ProposalID: "proposal-1", Status: "draft"}}, nil
}
func (*dialogStateStub) GetProposal(context.Context, string, agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	return agentsdk.AgentProposal{ProposalID: "proposal-1", Status: "draft"}, nil
}
func (*dialogStateStub) StoreProposal(context.Context, agentsdk.AgentProposal) (agentsdk.AgentProposal, error) {
	return agentsdk.AgentProposal{}, nil
}
func (*dialogStateStub) DecideProposal(context.Context, agentsdk.AgentProposalDecision, agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	return agentsdk.AgentProposal{}, nil
}

func TestSurfaceOwnsDialogStateRoutesAndUsesAuthenticatedIdentity(t *testing.T) {
	state := &dialogStateStub{session: agentsdk.AgentSession{ExternalSessionID: "session-1", Title: "Review"}}
	surface, err := NewSurface(surfaceBindingStub{state: state})
	if err != nil {
		t.Fatal(err)
	}
	if err := modulehttp.ValidateSurface(surface); err != nil {
		t.Fatal(err)
	}
	if surface.Owner() != "agent" || surface.Name() != "dialog_state" || len(surface.Routes()) != 10 {
		t.Fatalf("surface=%s/%s routes=%d", surface.Owner(), surface.Name(), len(surface.Routes()))
	}
	if operations := surface.(modulehttp.OpenAPIProvider).OpenAPIOperations(); len(operations) != len(surface.Routes()) {
		t.Fatalf("OpenAPI operations=%d routes=%d", len(operations), len(surface.Routes()))
	}
	for _, route := range surface.Routes() {
		if route.Pattern == "GET /operations/agent/tasks" {
			if len(route.Exposures) != 2 || route.Exposures[0] != modulehttp.ExposureTenantAdmin || route.Exposures[1] != modulehttp.ExposureOps || len(route.AnyPermissions) != 2 {
				t.Fatalf("operator route contract=%+v", route)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}}))
	response := httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "session-1") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if state.authority != (agentsdk.AgentAuthority{WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}) {
		t.Fatalf("authority=%+v", state.authority)
	}
}

func TestAgentOwnsOpenAPIForEveryHTTPRoute(t *testing.T) {
	operations := agentOpenAPIOperations()
	if len(operations) != 22 {
		t.Fatalf("Agent OpenAPI operations=%d", len(operations))
	}
	stream := operations["POST /agent-dialog/runs/stream"]
	responses := stream["responses"].(map[string]any)
	content := responses["200"].(map[string]any)["content"].(map[string]any)
	if stream["operationId"] != "runAgentStream" || stream["requestBody"] == nil || content["text/event-stream"] == nil {
		t.Fatalf("Agent stream OpenAPI=%#v", stream)
	}
	tool := operations["POST /agent-dialog/task-tools/invoke"]
	if security, ok := tool["security"].([]any); !ok || len(security) != 0 {
		t.Fatalf("tool callback must override inherited auth security: %#v", tool["security"])
	}
}

func TestSurfaceRejectsAmbientAuthorityAndInvalidJSON(t *testing.T) {
	surface, err := NewSurface(surfaceBindingStub{state: &dialogStateStub{}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("anonymous status=%d body=%s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/sessions", strings.NewReader("{"))
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}))
	response = httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSurfaceOwnsInteractiveAndPrincipalTaskReads(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	tasks := &taskStateSurfaceStub{run: agentmodel.AgentTaskRun{
		ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunSucceeded,
		Input: map[string]any{"secret": "hidden"}, Output: map[string]any{"result": "visible"}, CreatedAt: now, UpdatedAt: now,
		Identity: agentsdk.ExecutionIdentity{Initiator: agentsdk.PrincipalReference{WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator", AuthorizationRevision: "auth-1"}},
	}, found: true}
	interactive := &interactiveStateSurfaceStub{run: agentmodel.AgentInteractiveRun{ID: "interactive-1", WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}, found: true}
	surface, err := NewSurface(surfaceBindingStub{state: &dialogStateStub{}, tasks: tasks, interactive: interactive})
	if err != nil {
		t.Fatal(err)
	}
	identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator", AuthorizationRevision: "auth-1"}}

	request := httptest.NewRequest(http.MethodGet, "/agent-dialog/runs/interactive-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response := httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "interactive-1") {
		t.Fatalf("interactive status=%d body=%s", response.Code, response.Body.String())
	}
	if interactive.authority.WorkspaceID != "workspace-1" || interactive.authority.AuthorizationRevision != "auth-1" {
		t.Fatalf("interactive authority=%+v", interactive.authority)
	}

	request = httptest.NewRequest(http.MethodGet, "/agent-dialog/task-runs/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response = httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":"visible"`) || strings.Contains(response.Body.String(), "hidden") {
		t.Fatalf("task status=%d body=%s", response.Code, response.Body.String())
	}
	if tasks.workspaceID != "workspace-1" {
		t.Fatalf("task workspace=%q", tasks.workspaceID)
	}

	stale := identity
	stale.Principal.AuthorizationRevision = "auth-2"
	request = httptest.NewRequest(http.MethodGet, "/agent-dialog/task-runs/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), stale))
	response = httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("stale authorization status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSurfaceOwnsOperatorTaskQueries(t *testing.T) {
	tasks := &taskStateSurfaceStub{runs: []agentmodel.AgentTaskRun{{ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "review", Status: agentmodel.AgentTaskRunRunning}}, run: agentmodel.AgentTaskRun{ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "review", Status: agentmodel.AgentTaskRunRunning}, found: true}
	surface, err := NewSurface(surfaceBindingStub{state: &dialogStateStub{}, tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator", RoleKey: "admin"}}
	request := httptest.NewRequest(http.MethodGet, "/operations/agent/tasks?status=running&process_id=process-1&task_key=review&limit=7", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response := httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":1`) {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	if tasks.workspaceID != "workspace-1" || len(tasks.filter.Statuses) != 1 || tasks.filter.Statuses[0] != agentmodel.AgentTaskRunRunning || tasks.filter.ProcessID != "process-1" || tasks.filter.TaskKey != "review" || tasks.filter.Limit != 7 {
		t.Fatalf("filter=%+v workspace=%q", tasks.filter, tasks.workspaceID)
	}

	request = httptest.NewRequest(http.MethodGet, "/operations/agent/tasks/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response = httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "task-1") {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
}
