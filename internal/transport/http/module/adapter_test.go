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
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agenttestsupport "github.com/domainry/domainry-agent/testsupport"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type surfaceBindingStub struct {
	state       agentsdk.AgentDialogStateService
	tasks       agentpersistence.AgentTaskStateService
	interactive agentpersistence.AgentInteractiveStateService
}

type singleSurfaceProvider struct {
	adapter      modulehttp.Adapter
	conversation modulehttp.Adapter
}

func (provider singleSurfaceProvider) HTTPAdapters() []modulehttp.Adapter {
	if provider.conversation != nil {
		return []modulehttp.Adapter{provider.adapter, provider.conversation}
	}
	return []modulehttp.Adapter{provider.adapter}
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
	adapter, err := NewAdapter(surfaceBindingStub{state: state})
	if err != nil {
		t.Fatal(err)
	}
	if err := modulehttp.ValidateAdapter(adapter); err != nil {
		t.Fatal(err)
	}
	if adapter.Owner() != "agent" || adapter.Name() != "dialog_state" || len(adapter.Routes()) != 8 {
		t.Fatalf("adapter=%s/%s routes=%d", adapter.Owner(), adapter.Name(), len(adapter.Routes()))
	}
	request := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}}))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "session-1") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if state.authority != (agentsdk.AgentAuthority{WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}) {
		t.Fatalf("authority=%+v", state.authority)
	}
}

func TestFullSurfaceIsAnExactProjectionOfTheCompleteActionManifest(t *testing.T) {
	state := &dialogStateStub{}
	tasks := &taskStateSurfaceStub{}
	adapter, err := NewOwnedAdapter(surfaceBindingStub{state: state, tasks: tasks}, AdapterApplications{
		Interactive:    agentapplication.NewInteractiveExecutionService(agentapplication.InteractiveExecutionDependencies{}),
		Proposals:      agentapplication.NewProposalService(nil, nil, nil, nil),
		TaskOperations: agentapplication.NewTaskOperationsService(tasks, nil, nil),
		TaskTools:      agentapplication.NewTaskToolService(tasks, nil, nil),
		Analysis:       agentapplication.NewAnalysisService(nil, nil),
		Diagnostics:    agentapplication.NewDiagnosticsService(state, nil),
		DirectTasks:    agentapplication.NewDirectTaskExecutionService(nil, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := agentsdk.AgentAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 140+len(agentsdk.ConversationToolActions()) || len(adapter.Routes()) != 23 {
		t.Fatalf("actions=%d routes=%d", len(actions), len(adapter.Routes()))
	}
	conversation, err := NewConversationAdapter(conversationSurfaceStub{}, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := modulehttp.ValidateAuthorizationProjection(actions, singleSurfaceProvider{adapter: adapter, conversation: conversation}); err != nil {
		t.Fatal(err)
	}
}

func TestSurfaceRejectsAmbientAuthorityAndInvalidJSON(t *testing.T) {
	adapter, err := NewAdapter(surfaceBindingStub{state: &dialogStateStub{}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/agent/sessions", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("anonymous status=%d body=%s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/sessions", strings.NewReader("{"))
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}))
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
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
	adapter, err := NewAdapter(surfaceBindingStub{state: &dialogStateStub{}, tasks: tasks, interactive: interactive})
	if err != nil {
		t.Fatal(err)
	}
	identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator", AuthorizationRevision: "auth-1"}}

	request := httptest.NewRequest(http.MethodGet, "/agent/runs/interactive-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "interactive-1") {
		t.Fatalf("interactive status=%d body=%s", response.Code, response.Body.String())
	}
	if interactive.authority.WorkspaceID != "workspace-1" || interactive.authority.AuthorizationRevision != "auth-1" {
		t.Fatalf("interactive authority=%+v", interactive.authority)
	}

	request = httptest.NewRequest(http.MethodGet, "/agent/task-runs/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":"visible"`) || strings.Contains(response.Body.String(), "hidden") {
		t.Fatalf("task status=%d body=%s", response.Code, response.Body.String())
	}
	if tasks.workspaceID != "workspace-1" {
		t.Fatalf("task workspace=%q", tasks.workspaceID)
	}

	stale := identity
	stale.Principal.AuthorizationRevision = "auth-2"
	request = httptest.NewRequest(http.MethodGet, "/agent/task-runs/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), stale))
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("stale authorization status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSurfaceOwnsOperatorTaskQueries(t *testing.T) {
	tasks := &taskStateSurfaceStub{runs: []agentmodel.AgentTaskRun{{ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "review", Status: agentmodel.AgentTaskRunRunning}}, run: agentmodel.AgentTaskRun{ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "review", Status: agentmodel.AgentTaskRunRunning}, found: true}
	adapter, err := NewOwnedAdapter(surfaceBindingStub{state: &dialogStateStub{}, tasks: tasks}, AdapterApplications{TaskOperations: agentapplication.NewTaskOperationsService(tasks, nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range adapter.Routes() {
		if route.Action.Key == agentsdk.ActionAgentTasksList && (len(route.Action.Exposures) != 2 || route.Action.Permission == nil || route.Action.Permission.Key != route.Action.Key) {
			t.Fatalf("operator route contract=%+v", route)
		}
	}
	identity := actionRequestIdentity(agentsdk.ActionAgentTasksList)
	request := httptest.NewRequest(http.MethodGet, "/agent/tasks?status=running&process_id=process-1&task_key=review&limit=7", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":1`) {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	if tasks.workspaceID != "workspace-1" || len(tasks.filter.Statuses) != 1 || tasks.filter.Statuses[0] != agentmodel.AgentTaskRunRunning || tasks.filter.ProcessID != "process-1" || tasks.filter.TaskKey != "review" || tasks.filter.Limit != 7 {
		t.Fatalf("filter=%+v workspace=%q", tasks.filter, tasks.workspaceID)
	}

	request = httptest.NewRequest(http.MethodGet, "/agent/tasks/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("list grant reached get Action: status=%d body=%s", response.Code, response.Body.String())
	}

	identity = actionRequestIdentity(agentsdk.ActionAgentTasksGet)
	request = httptest.NewRequest(http.MethodGet, "/agent/tasks/task-1", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "task-1") {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
}

func actionRequestIdentity(actionKey string) identitysdk.RequestIdentity {
	separator := strings.LastIndex(actionKey, ".")
	bundle := identitysdk.AccessBundle{FunctionGrants: []identitysdk.FunctionGrant{{
		Resource: identitysdk.ResourceType(actionKey[:separator]), Action: identitysdk.Action(actionKey[separator+1:]), Effect: identitysdk.EffectAllow,
	}}, DataPolicies: []identitysdk.DataPolicy{{
		Resource: identitysdk.ResourceType(actionKey[:separator]), Action: identitysdk.Action(actionKey[separator+1:]), Effect: identitysdk.EffectAllow,
	}}}
	return identitysdk.RequestIdentity{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-1", UserID: "operator", RoleKey: "admin", AccessBundle: &bundle,
	}}
}

// The projection check needs metadata only; invocation is covered separately.
type conversationSurfaceStub struct{ agentsdk.ConversationService }

type conversationSourceSurfaceStub struct {
	agentsdk.ConversationService
	request agentsdk.ConversationSourceVerificationRequest
}

func (stub *conversationSourceSurfaceStub) VerifyConversationSources(_ context.Context, request agentsdk.ConversationSourceVerificationRequest) (agentsdk.ConversationSourceVerificationReceipt, error) {
	stub.request = request
	return agentsdk.ConversationSourceVerificationReceipt{
		WorkspaceID: request.Reader.WorkspaceID, References: request.References, SourceIDs: request.SourceIDs,
		VerifiedAt: time.Now().UTC(),
	}, nil
}

func TestConversationSourceVerificationRouteOverwritesReaderAuthority(t *testing.T) {
	service := &conversationSourceSurfaceStub{}
	adapter, err := NewConversationAdapter(service, "runtime-owner")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/conversation-sources/verify", strings.NewReader(`{
		"references":[{"conversation_id":"conversation-a","run_id":"run-a","before_step":2}],
		"source_ids":["conversation://conversation-a/turn/run-a"],
		"reader":{"known":true,"runtime_id":"spoofed","workspace_id":"spoofed","user_id":"spoofed"}
	}`))
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-a", UserID: "service-a", RoleKey: "delivery",
	}}))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.request.Reader.RuntimeID != "runtime-owner" || service.request.Reader.WorkspaceID != "workspace-a" || service.request.Reader.UserID != "service-a" || service.request.Reader.RoleKey != "delivery" {
		t.Fatalf("status=%d body=%s request=%+v", response.Code, response.Body.String(), service.request)
	}
}

type conversationSkillSurfaceStub struct {
	agentsdk.ConversationService
	agentsdk.ConversationSkillService
	authority    agentsdk.ConversationAuthority
	skillKey     string
	skillVersion string
	resourceKey  string
	candidateID  string
	candidate    agentsdk.ConversationImprovementCandidateCreate
	evaluation   agentsdk.ConversationImprovementEvaluationWrite
}

func (s *conversationSkillSurfaceStub) ConversationSkill(_ context.Context, key, version string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationSkillVersion, error) {
	s.authority, s.skillKey, s.skillVersion = authority, key, version
	return agentsdk.ConversationSkillVersion{Definition: agentsdk.SkillSchema{Key: key, Version: version, Instructions: "exact body"}}, nil
}

func (s *conversationSkillSurfaceStub) ConversationSkillResource(_ context.Context, key, version, resource string, authority agentsdk.ConversationAuthority) (agentsdk.SkillResource, error) {
	s.authority, s.skillKey, s.skillVersion, s.resourceKey = authority, key, version, resource
	return agentsdk.SkillResource{Key: resource, Content: "exact resource"}, nil
}

func (s *conversationSkillSurfaceStub) CreateConversationImprovementCandidate(_ context.Context, in agentsdk.ConversationImprovementCandidateCreate, authority agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	s.authority, s.candidate = authority, in
	return agentsdk.ConversationImprovementCandidate{ID: "candidate-one", Kind: in.Kind, TargetKey: in.TargetKey, Version: in.Version, FeedbackIDs: in.FeedbackIDs, Proposal: in.Proposal, Reason: in.Reason}, nil
}

func (s *conversationSkillSurfaceStub) EvaluateConversationImprovementCandidate(_ context.Context, id string, in agentsdk.ConversationImprovementEvaluationWrite, authority agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	s.authority, s.candidateID, s.evaluation = authority, id, in
	return agentsdk.ConversationImprovementCandidate{ID: id, Revision: in.ExpectedRevision + 1, Evaluation: &agentsdk.ConversationImprovementEvaluation{SuiteVersion: in.SuiteVersion}}, nil
}

func TestConversationSkillHTTPRoutesPreserveExactPathsBodiesAndIdentity(t *testing.T) {
	service := &conversationSkillSurfaceStub{}
	adapter, err := NewConversationAdapter(service, "runtime-one")
	if err != nil {
		t.Fatal(err)
	}
	identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-one", UserID: "user-one", RoleKey: "admin"}}
	serve := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
		response := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(response, request)
		return response
	}
	response := serve(http.MethodGet, "/agent/skills/report/versions/2", "")
	if response.Code != http.StatusOK || service.skillKey != "report" || service.skillVersion != "2" || !strings.Contains(response.Body.String(), "exact body") {
		t.Fatalf("Skill response=%d body=%s key=%q version=%q", response.Code, response.Body.String(), service.skillKey, service.skillVersion)
	}
	response = serve(http.MethodGet, "/agent/skills/report/versions/2/resources/template", "")
	if response.Code != http.StatusOK || service.resourceKey != "template" || !strings.Contains(response.Body.String(), "exact resource") {
		t.Fatalf("resource response=%d body=%s resource=%q", response.Code, response.Body.String(), service.resourceKey)
	}
	response = serve(http.MethodPost, "/agent/improvement-candidates", `{"client_id":"create-one","kind":"skill","target_key":"report","version":"3","feedback_ids":["feedback-one"],"proposal":{"key":"report","version":"3"},"reason":"reviewed change"}`)
	if response.Code != http.StatusOK || service.candidate.ClientID != "create-one" || service.candidate.TargetKey != "report" || len(service.candidate.FeedbackIDs) != 1 {
		t.Fatalf("candidate response=%d body=%s input=%+v", response.Code, response.Body.String(), service.candidate)
	}
	response = serve(http.MethodPost, "/agent/improvement-candidates/candidate-one/evaluation", `{"client_id":"evaluate-one","expected_revision":1,"suite_version":"v01-skill","scenario_ids":["scenario-one"],"baseline_completed":0,"candidate_completed":1,"baseline_omissions":1,"candidate_omissions":0,"passed":true}`)
	if response.Code != http.StatusOK || service.candidateID != "candidate-one" || service.evaluation.SuiteVersion != "v01-skill" || service.authority.RuntimeID != "runtime-one" || service.authority.WorkspaceID != "workspace-one" || service.authority.UserID != "user-one" {
		t.Fatalf("evaluation response=%d body=%s candidate=%q input=%+v authority=%+v", response.Code, response.Body.String(), service.candidateID, service.evaluation, service.authority)
	}
}

type conversationEventSurfaceStub struct{ agentsdk.ConversationService }

func (conversationEventSurfaceStub) Events(context.Context, string, string, int64, int, agentsdk.ConversationAuthority) (agentsdk.ConversationEventPage, error) {
	return agentsdk.ConversationEventPage{Terminal: true}, nil
}

type unwrappingResponseWriter struct{ http.ResponseWriter }

func (w *unwrappingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestConversationStreamPreservesFlusherThroughHostWriterWrappers(t *testing.T) {
	adapter, err := NewConversationAdapter(conversationEventSurfaceStub{}, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/agent/conversations/conversation-1/runs/run-1/events/stream", nil)
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-1", UserID: "operator", RoleKey: "admin",
	}}))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(&unwrappingResponseWriter{ResponseWriter: response}, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || response.Body.String() != ": keepalive\n\n" {
		t.Fatalf("status=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}
