package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

type ConversationOptions struct {
	DocumentStorage                                           agentsdk.KnowledgeDocumentStorage
	DocumentPoll                                              time.Duration
	LibraryKnowledge                                          []LibraryKnowledgeBinding
	LibraryAuthorizer                                         agentsdk.KnowledgeLibraryAuthorizer
	AttachmentStorage                                         agentsdk.ConversationAttachmentStorage
	AttachmentAuthorizer                                      agentsdk.ConversationAttachmentAuthorizer
	Business                                                  agentsdk.ConversationBusinessSource
	ArtifactStorage                                           agentsdk.ConversationArtifactStorage
	ArtifactExportTTL                                         time.Duration
	PersonalAuthorizer                                        agentsdk.ConversationToolAuthorizer
	ToolHost                                                  agentsdk.ConversationToolHost
	ToolAvailability                                          agentsdk.ConversationToolAvailability
	MaxSteps, MaxToolCalls, MaxArgumentBytes                  int
	Knowledge                                                 ConversationKnowledge
	KnowledgeBytes                                            int
	ContextBytes, MaxInputBytes, MaxOutputBytes, SummaryBytes int
	Workers                                                   int
	Lease, Poll, RunTimeout                                   time.Duration
	InteractionTTL                                            time.Duration
}

type conversationActive struct {
	cancel context.CancelFunc
	fence  int64
}
type ConversationService struct {
	repo             agentpersistence.ConversationRepository
	model            agentsdk.ConversationModel
	runtimeID, owner string
	options          ConversationOptions
	wake             chan struct{}
	attachmentWake   chan struct{}
	documentWake     chan struct{}
	cancel           context.CancelFunc
	lifetime         context.Context
	wg               sync.WaitGroup
	mu               sync.Mutex
	active           map[string]conversationActive
}

func NewConversationService(repo agentpersistence.ConversationRepository, model agentsdk.ConversationModel, runtimeID string, options ConversationOptions) (*ConversationService, error) {
	if repo == nil || strings.TrimSpace(runtimeID) == "" || len(runtimeID) > 255 {
		return nil, fmt.Errorf("conversation repository and runtime identity are required")
	}
	if options.LibraryAuthorizer != nil {
		if _, ok := repo.(agentpersistence.KnowledgeLibraryRepository); !ok {
			return nil, fmt.Errorf("knowledge library authorization requires library persistence")
		}
	}
	if options.AttachmentStorage != nil {
		if _, ok := repo.(agentpersistence.ConversationAttachmentRepository); !ok || options.AttachmentAuthorizer == nil {
			return nil, fmt.Errorf("attachment storage requires attachment persistence and current authorization")
		}
	}
	if options.DocumentStorage != nil {
		if _, ok := repo.(agentpersistence.KnowledgeDocumentRepository); !ok || options.LibraryAuthorizer == nil {
			return nil, fmt.Errorf("document storage requires document persistence and library authorization")
		}
	}
	if options.DocumentPoll == 0 {
		options.DocumentPoll = 2 * time.Second
	}
	if options.DocumentPoll < 10*time.Millisecond || options.DocumentPoll > time.Minute {
		return nil, fmt.Errorf("invalid document polling interval")
	}
	if options.ArtifactExportTTL == 0 {
		options.ArtifactExportTTL = time.Hour
	}
	if options.ArtifactExportTTL < time.Second || options.ArtifactExportTTL > 24*time.Hour || options.ArtifactExportTTL%time.Second != 0 {
		return nil, fmt.Errorf("invalid artifact export lifetime")
	}
	if options.ContextBytes == 0 {
		options.ContextBytes = 65536
	}
	if options.MaxInputBytes == 0 {
		options.MaxInputBytes = 16384
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = 8192
	}
	if options.SummaryBytes == 0 {
		options.SummaryBytes = 4096
	}
	if options.Workers == 0 {
		options.Workers = 2
	}
	if options.Lease == 0 {
		options.Lease = 30 * time.Second
	}
	if options.Poll == 0 {
		options.Poll = time.Second
	}
	if options.RunTimeout == 0 {
		options.RunTimeout = 5 * time.Minute
	}
	if options.InteractionTTL == 0 {
		options.InteractionTTL = 24 * time.Hour
	}
	if options.InteractionTTL < time.Millisecond || options.InteractionTTL > 7*24*time.Hour {
		return nil, fmt.Errorf("invalid conversation interaction lifetime")
	}
	if options.MaxSteps == 0 {
		options.MaxSteps = 12
	}
	if options.MaxToolCalls == 0 {
		options.MaxToolCalls = execution.DefaultMaxToolCalls
	}
	if options.MaxArgumentBytes == 0 {
		options.MaxArgumentBytes = 16 * 1024
	}
	if options.MaxSteps < 1 || options.MaxSteps > 256 || options.MaxToolCalls < 1 || options.MaxToolCalls > 64 || options.MaxArgumentBytes < 1 || options.MaxArgumentBytes > 1024*1024 {
		return nil, fmt.Errorf("invalid conversation execution limits")
	}
	if options.ToolHost != nil {
		if _, ok := model.(agentsdk.ConversationAgentModel); !ok {
			return nil, fmt.Errorf("conversation tool host requires a tool-capable model")
		}
		if _, ok := repo.(agentpersistence.ConversationExecutionRepository); !ok {
			return nil, fmt.Errorf("conversation tool host requires execution persistence")
		}
		if options.ToolAvailability == nil {
			options.ToolAvailability, _ = options.ToolHost.(agentsdk.ConversationToolAvailability)
		}
	} else if options.ToolAvailability != nil {
		return nil, fmt.Errorf("conversation tool availability requires a tool host")
	}
	if documents, ok := repo.(agentpersistence.KnowledgeDocumentRepository); ok && options.Knowledge != nil {
		if source, ok := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); ok {
			if _, ok := options.Knowledge.(agentsdk.ConversationKnowledgeSource); !ok {
				return nil, fmt.Errorf("managed document source requires knowledge revalidation")
			}
			options.Knowledge = &documentGuardedKnowledge{base: options.Knowledge, source: source, repo: documents}
		}
	}
	if len(options.LibraryKnowledge) > 0 {
		if options.ToolHost == nil {
			return nil, fmt.Errorf("library knowledge retrieval requires a tool-capable conversation host")
		}
		source, err := newLibraryKnowledgeSource(repo, runtimeID, options.LibraryAuthorizer, options.LibraryKnowledge, options.Knowledge)
		if err != nil {
			return nil, err
		}
		options.Knowledge = source
	}
	var personalHost *PersonalConversationHost
	if personal, ok := options.ToolHost.(*PersonalConversationHost); ok {
		copy := *personal
		copy.knowledge = options.Knowledge
		personalHost = &copy
		options.ToolHost = &copy
	}
	if options.Business != nil {
		if options.ToolHost == nil || options.PersonalAuthorizer == nil {
			return nil, fmt.Errorf("business conversations require a tool host and current action authorization")
		}
		if _, ok := repo.(agentpersistence.ConversationSourceRepository); !ok {
			return nil, fmt.Errorf("business conversations require source access persistence")
		}
		if source := options.Business.BusinessSourceIdentity(); strings.TrimSpace(source) == "" || len(source) > 2048 {
			return nil, fmt.Errorf("business source identity is required")
		}
		options.ToolHost = &businessConversationHost{base: options.ToolHost, authorizer: options.PersonalAuthorizer, source: options.Business, repo: repo}
	}
	if options.Knowledge != nil {
		if _, ok := repo.(agentpersistence.ConversationSourceRepository); !ok {
			return nil, fmt.Errorf("knowledge conversations require source access persistence")
		}
		if options.KnowledgeBytes == 0 {
			options.KnowledgeBytes = options.ContextBytes / 4
		}
		if options.KnowledgeBytes < 256 || options.KnowledgeBytes+options.MaxInputBytes+options.SummaryBytes+2048 >= options.ContextBytes {
			return nil, fmt.Errorf("invalid conversation knowledge budget")
		}
		if options.ToolHost != nil {
			source, ok := options.Knowledge.(agentsdk.ConversationKnowledgeSource)
			if !ok || options.PersonalAuthorizer == nil {
				return nil, fmt.Errorf("knowledge tools require a revalidating knowledge source and a host authorizer")
			}
			options.ToolHost = &knowledgeConversationHost{base: options.ToolHost, authorizer: options.PersonalAuthorizer, source: source, repo: repo}
		}
	}
	if options.ContextBytes < 4096 || options.MaxInputBytes < 1 || options.MaxOutputBytes < 1 || options.SummaryBytes < 256 || options.MaxInputBytes+options.SummaryBytes+1024 >= options.ContextBytes || options.Workers < 1 || options.Workers > 32 || options.Lease < 300*time.Millisecond || options.Poll <= 0 || options.RunTimeout <= 0 {
		return nil, fmt.Errorf("invalid conversation limits")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	if err := activateDocumentSources(repo, runtimeID, options); err != nil {
		return nil, err
	}
	s := &ConversationService{repo: repo, model: model, runtimeID: runtimeID, owner: hex.EncodeToString(b[:]), options: options, wake: make(chan struct{}, options.Workers), attachmentWake: make(chan struct{}, 1), active: map[string]conversationActive{}}
	s.documentWake = make(chan struct{}, 1)
	if personalHost != nil {
		personalHost.artifacts = s
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.lifetime = ctx
	if options.DocumentStorage != nil {
		s.wg.Add(1)
		go s.knowledgeDocumentWorker(ctx, repo.(agentpersistence.KnowledgeDocumentRepository))
	}
	if options.AttachmentStorage != nil {
		s.wg.Add(1)
		go s.cleanupAttachments(ctx, repo.(agentpersistence.ConversationAttachmentRepository))
	}
	if interactions, ok := repo.(agentpersistence.ConversationInteractionRepository); ok {
		s.wg.Add(1)
		go s.expireConversationInteractions(ctx, interactions)
	}
	if model != nil {
		for i := 0; i < options.Workers; i++ {
			s.wg.Add(1)
			go s.worker(ctx)
		}
	}
	return s, nil
}
func (s *ConversationService) Close() { s.cancel(); s.wg.Wait() }

func (s *ConversationService) ConversationReady(ctx context.Context) error {
	if s.model == nil {
		return conversationFailure("unavailable", "model_not_configured")
	}
	if s.lifetime.Err() != nil {
		return conversationFailure("unavailable", "worker_stopped")
	}
	if err := s.repo.Ready(ctx); err != nil {
		return conversationFailure("unavailable", "persistence_unavailable")
	}
	return nil
}
func (s *ConversationService) ConversationStreaming() bool {
	if s.options.ToolHost != nil {
		return true
	}
	_, ok := s.model.(agentsdk.ConversationStreamingModel)
	return ok
}
func (s *ConversationService) ConversationExecutionEnabled() bool { return s.options.ToolHost != nil }
func conversationFailure(class, code string) error {
	return &agentsdk.Error{Class: class, Code: "agent.conversation." + code}
}
func (s *ConversationService) authorize(a agentsdk.ConversationAuthority) error {
	if !a.Known || a.RuntimeID != s.runtimeID || strings.TrimSpace(a.UserID) == "" || strings.TrimSpace(a.WorkspaceID) == "" || len(a.UserID) > 255 || len(a.WorkspaceID) > 255 {
		return conversationFailure("forbidden", "principal_required")
	}
	return nil
}
func conversationText(v string, max int, required bool) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0) && len(v) <= max && (!required || strings.TrimSpace(v) != "")
}
func conversationKey(v string) bool {
	if len(v) < 1 || len(v) > 96 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}
func (s *ConversationService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ConversationService) Create(ctx context.Context, in agentsdk.ConversationCreate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.Conversation{}, err
	}
	if !conversationKey(in.ClientID) || !conversationText(in.Title, 512, false) {
		return agentsdk.Conversation{}, conversationFailure("bad_request", "create_invalid")
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = "新会话"
	}
	return s.repo.Create(ctx, in, a)
}
func (s *ConversationService) List(ctx context.Context, in agentsdk.ConversationQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationPage, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationPage{}, err
	}
	if !conversationText(in.Search, 512, false) || len(in.BeforeID) > 256 || in.Limit < 0 || in.Limit > 100 {
		return agentsdk.ConversationPage{}, conversationFailure("bad_request", "query_invalid")
	}
	return s.repo.List(ctx, in, a)
}
func (s *ConversationService) Get(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.Conversation{}, err
	}
	return s.repo.Get(ctx, id, a)
}
func (s *ConversationService) Update(ctx context.Context, id string, in agentsdk.ConversationUpdate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.Conversation{}, err
	}
	if in.ExpectedRevision < 1 || in.Title != nil && !conversationText(*in.Title, 512, true) {
		return agentsdk.Conversation{}, conversationFailure("bad_request", "update_invalid")
	}
	return s.repo.Update(ctx, id, in, a)
}
func (s *ConversationService) Delete(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	if err := s.authorize(a); err != nil {
		return err
	}
	if revision < 1 {
		return conversationFailure("bad_request", "revision_required")
	}
	err := s.repo.Delete(ctx, id, revision, a)
	if err == nil {
		s.wakeAttachmentCleanup()
	}
	return err
}
func (s *ConversationService) Send(ctx context.Context, id string, in agentsdk.ConversationSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	if !conversationKey(in.ClientMessageID) || !conversationText(in.Message, s.options.MaxInputBytes, true) {
		return agentsdk.ConversationRun{}, conversationFailure("bad_request", "message_invalid")
	}
	if s.model == nil {
		return agentsdk.ConversationRun{}, conversationFailure("unavailable", "model_not_configured")
	}
	out, err := s.repo.Enqueue(ctx, id, in, a)
	if err == nil {
		s.signal()
		out = s.projectConversationRun(ctx, out, a)
	}
	return out, err
}
func (s *ConversationService) Messages(ctx context.Context, id string, in agentsdk.ConversationMessageQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationMessagePage, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationMessagePage{}, err
	}
	if in.AfterSeq < 0 || in.BeforeSeq < 0 || in.Limit < 0 || in.Limit > 100 || in.BeforeSeq > 0 && in.AfterSeq >= in.BeforeSeq {
		return agentsdk.ConversationMessagePage{}, conversationFailure("bad_request", "query_invalid")
	}
	out, err := s.repo.Messages(ctx, id, in, a)
	if err != nil {
		return out, err
	}
	if _, ok := s.repo.(agentpersistence.ConversationSourceRepository); ok {
		ctx, cancel := s.sourceAccessContext(ctx)
		defer cancel()
		audit := s.sourceAudit(a)
		for i, message := range out.Items {
			out.Items[i].Citations = nil
			if message.Role != "assistant" || message.RunID == "" {
				continue
			}
			if _, err := audit.run(ctx, agentsdk.ConversationRunReference{ConversationID: message.ConversationID, RunID: message.RunID}); err != nil {
				out.Items[i].Content = unavailableHistory
				out.Items[i].AccessError = sourceAccessCode(err)
				continue
			}
			run, err := s.repo.Run(ctx, message.ConversationID, message.RunID, a)
			if err != nil {
				return out, err
			}
			out.Items[i].Citations = conversationCitations(run, message.Content)
		}
	}
	return out, nil
}
func (s *ConversationService) Run(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	out, err := s.repo.Run(ctx, id, run, a)
	if err == nil {
		out = s.projectConversationRun(ctx, out, a)
	}
	return out, err
}
func (s *ConversationService) Events(ctx context.Context, id, run string, after int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationEventPage, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationEventPage{}, err
	}
	if after < 0 || limit < 0 || limit > 100 {
		return agentsdk.ConversationEventPage{}, conversationFailure("bad_request", "query_invalid")
	}
	out, err := s.repo.Events(ctx, id, run, after, limit, a)
	if err != nil {
		return out, err
	}
	if _, ok := s.repo.(agentpersistence.ConversationSourceRepository); ok {
		ctx, cancel := s.sourceAccessContext(ctx)
		defer cancel()
		if _, err := s.sourceAudit(a).run(ctx, agentsdk.ConversationRunReference{ConversationID: id, RunID: run}); err != nil {
			return agentsdk.ConversationEventPage{}, conversationFailure("forbidden", sourceAccessCode(err))
		}
	}
	return out, nil
}
func (s *ConversationService) Cancel(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	out, err := s.repo.Cancel(ctx, id, run, a)
	if err == nil {
		s.mu.Lock()
		if active, ok := s.active[run]; ok {
			active.cancel()
		}
		s.mu.Unlock()
	}
	if err == nil {
		out = s.projectConversationRun(ctx, out, a)
	}
	return out, err
}
func (s *ConversationService) Resume(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	if s.model == nil {
		return agentsdk.ConversationRun{}, conversationFailure("unavailable", "model_not_configured")
	}
	out, err := s.repo.Resume(ctx, id, run, a)
	if err == nil {
		s.signal()
		out = s.projectConversationRun(ctx, out, a)
	}
	return out, err
}
func (s *ConversationService) Memories(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	if err := s.authorize(a); err != nil {
		return nil, err
	}
	return s.repo.Memories(ctx, a)
}
func (s *ConversationService) WriteMemory(ctx context.Context, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationMemory{}, err
	}
	if !conversationKey(in.ID) || !conversationText(in.Title, 128, true) || !conversationText(in.Content, 512, true) || in.ExpectedRevision < 0 {
		return agentsdk.ConversationMemory{}, conversationFailure("bad_request", "memory_invalid")
	}
	return s.repo.WriteMemory(ctx, in, a)
}
func (s *ConversationService) DeleteMemory(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	if err := s.authorize(a); err != nil {
		return err
	}
	if !conversationKey(id) || revision < 1 {
		return conversationFailure("bad_request", "memory_invalid")
	}
	return s.repo.DeleteMemory(ctx, id, revision, a)
}

func (s *ConversationService) worker(ctx context.Context) {
	defer s.wg.Done()
	for ctx.Err() == nil {
		claim, ok, err := s.repo.Claim(ctx, s.runtimeID, s.owner, s.options.Lease)
		if err == nil && ok {
			s.execute(ctx, claim)
			continue
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("conversation claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(s.options.Poll):
		}
	}
}
func (s *ConversationService) execute(parent context.Context, claim agentpersistence.ConversationClaim) {
	ctx, cancel := context.WithTimeout(parent, s.options.RunTimeout)
	defer cancel()
	// Keys are generated globally, while storage still fences every scoped write.
	s.mu.Lock()
	s.active[claim.Run.ID] = conversationActive{cancel: cancel, fence: claim.Fence}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.active[claim.Run.ID].fence == claim.Fence {
			delete(s.active, claim.Run.ID)
		}
		s.mu.Unlock()
	}()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(s.options.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := s.repo.Heartbeat(ctx, claim, s.options.Lease)
				if err != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	input, found, err := s.repo.ModelInput(ctx, claim, nil)
	if err == nil && !found {
		input, err = s.buildConversationContext(ctx, claim)
		if err == nil {
			input, _, err = s.repo.ModelInput(ctx, claim, &input)
		}
	}
	if err == nil {
		if found {
			err = s.checkRunSources(ctx, agentsdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID}, claim.Authority)
		} else {
			_, err = s.sourceAudit(claim.Authority).sources(ctx, input.Sources)
		}
	}
	if err == nil && conversationContextSize(input.Messages) > s.options.ContextBytes {
		err = fmt.Errorf("frozen context exceeds current configured budget")
	}
	var result agentsdk.ConversationModelResult
	code := ""
	if err != nil {
		code = conversationModelFailureCode(err, "context_failed")
	} else {
		if s.options.ToolHost != nil {
			result, err = s.generateConversationExecution(ctx, claim, input)
		} else {
			result, err = s.generateConversationReply(ctx, claim, input)
		}
		if errors.Is(err, errConversationWaiting) {
			return
		}
		if err != nil {
			code = conversationModelFailureCode(err, "provider_failed")
		} else if !conversationText(result.Content, s.options.MaxOutputBytes, true) {
			code = "response_invalid"
		}
		if _, encodeErr := json.Marshal(result); encodeErr != nil {
			code = "response_invalid"
		}
	}
	if parent.Err() != nil {
		return
	} // release by lease expiry after graceful shutdown
	if ctx.Err() != nil {
		code = "execution_interrupted"
	}
	if code != "" {
		result = agentsdk.ConversationModelResult{}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer finishCancel()
	if err := s.repo.Finish(finishCtx, claim, result, code); err != nil {
		slog.Debug("conversation finish fenced or unavailable", "run_id", claim.Run.ID, "error", err)
	}
}

var _ agentsdk.ConversationService = (*ConversationService)(nil)
var _ agentsdk.ConversationStatusProvider = (*ConversationService)(nil)

// Preserve only our stable provider categories; arbitrary model errors cannot
// inject provider text, keys or unbounded error strings into a persisted run.
func conversationModelFailureCode(err error, fallback string) string {
	var failure *agentsdk.Error
	if errors.As(err, &failure) {
		code := strings.TrimPrefix(failure.Code, "agent.conversation.")
		switch code {
		case "source_access_unavailable", "source_reference_invalid", "source_read_unavailable", "source_limit_exceeded", "source_snapshot_changed":
			return code
		case "model_changed", "execution_limit", "execution_context_exceeded", "tool_catalog_invalid", "tool_access_denied", "tool_unavailable", "tool_availability_failed", "tool_changed", "tool_confirmation_required", "tool_result_uncertain", "tool_result_invalid", "interaction_unavailable", "interaction_closed", "interaction_expired", "interaction_access_denied", "question_must_be_separate":
			return code
		case "execution_reference_invalid", "execution_reference_changed", "execution_read_unavailable", "result_reference_invalid", "result_reference_changed", "result_not_found", "result_read_unavailable", "tool_call_not_found", "run_not_found":
			return code
		case "knowledge_access_denied", "knowledge_not_found", "knowledge_quota_exhausted", "knowledge_rate_limited", "knowledge_timeout", "knowledge_network", "knowledge_request_invalid", "knowledge_unavailable", "knowledge_failed", "knowledge_response_invalid", "knowledge_context_exceeded", "knowledge_source_changed":
			return code
		case "business_access_denied", "business_record_not_found", "business_request_invalid", "business_source_changed", "business_unavailable", "business_response_invalid":
			return code
		case "provider_access_denied", "provider_quota_exhausted", "provider_rate_limited", "provider_timeout", "provider_network", "provider_request_invalid", "provider_unavailable":
			return code
		}
	}
	return fallback
}
