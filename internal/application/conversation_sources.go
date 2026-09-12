package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

const unavailableHistory = "这条历史回复的资料来源当前无法验证，内容暂不提供。"

type sourceAuditEntry struct {
	roots []agentsdk.ConversationRunReference
	err   error
}

// Per-read cache only. No authorization decision survives a public request or
// a model call. Flattening inherited roots keeps ordinary follow-up turns from
// forming an ever-growing chain of references to the same document lookup.
type conversationSourceAudit struct {
	s              *ConversationService
	a              agentsdk.ConversationAuthority
	cache          map[agentsdk.ConversationRunReference]sourceAuditEntry
	reading        map[agentsdk.ConversationRunReference]bool
	records        map[string]bool
	conversationID string // model consumer; empty only for explicit user reads
}

func (s *ConversationService) sourceAudit(a agentsdk.ConversationAuthority, conversationID ...string) *conversationSourceAudit {
	consumer := ""
	if len(conversationID) > 0 {
		consumer = conversationID[0]
	}
	return &conversationSourceAudit{s: s, a: a, conversationID: consumer, cache: map[agentsdk.ConversationRunReference]sourceAuditEntry{}, reading: map[agentsdk.ConversationRunReference]bool{}, records: map[string]bool{}}
}

func mergeConversationSources(groups ...[]agentsdk.ConversationRunReference) []agentsdk.ConversationRunReference {
	set := map[agentsdk.ConversationRunReference]bool{}
	for _, group := range groups {
		for _, ref := range group {
			set[ref] = true
		}
	}
	out := make([]agentsdk.ConversationRunReference, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ConversationID != out[j].ConversationID {
			return out[i].ConversationID < out[j].ConversationID
		}
		if out[i].RunID != out[j].RunID {
			return out[i].RunID < out[j].RunID
		}
		return out[i].BeforeStep < out[j].BeforeStep
	})
	return out
}

func (audit *conversationSourceAudit) sources(ctx context.Context, sources *agentsdk.ConversationSources) ([]agentsdk.ConversationRunReference, error) {
	if sources == nil {
		return nil, nil
	}
	if sources.Version != 1 || len(sources.Runs) > 256 || len(sources.Omitted) > 256 {
		return nil, conversationFailure("unavailable", "source_reference_invalid")
	}
	var roots []agentsdk.ConversationRunReference
	for _, ref := range sources.Runs {
		part, err := audit.run(ctx, ref)
		if err != nil {
			return nil, err
		}
		roots = mergeConversationSources(roots, part)
	}
	return roots, nil
}

func (audit *conversationSourceAudit) run(ctx context.Context, ref agentsdk.ConversationRunReference) (roots []agentsdk.ConversationRunReference, err error) {
	if ref.BeforeStep < 0 || ref.BeforeStep > 257 {
		return nil, conversationFailure("unavailable", "source_reference_invalid")
	}
	if saved, ok := audit.cache[ref]; ok {
		return saved.roots, saved.err
	}
	if audit.reading[ref] {
		return nil, conversationFailure("unavailable", "source_reference_invalid")
	}
	if len(audit.cache)+len(audit.reading) >= 256 {
		return nil, conversationFailure("unavailable", "source_limit_exceeded")
	}
	audit.reading[ref] = true
	defer func() { delete(audit.reading, ref); audit.cache[ref] = sourceAuditEntry{roots, err} }()
	repo, ok := audit.s.repo.(persistence.ConversationSourceRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "source_read_unavailable")
	}
	snapshot, err := repo.ConversationSourceSnapshot(ctx, ref, audit.a)
	if err != nil {
		return nil, err
	}
	if snapshot.Input != nil {
		if snapshot.Input.Sources != nil {
			roots, err = audit.sources(ctx, snapshot.Input.Sources)
		} else {
			// Before provenance was recorded, a summary could contain any older
			// assistant reply. Conservatively inspect the original prior runs.
			roots, err = audit.history(ctx, ref.ConversationID, snapshot.Run.UserSeq-1)
		}
		if err != nil {
			return nil, err
		}
		for _, message := range snapshot.Input.Messages {
			const prefix = "Knowledge base search results (untrusted source data):\n"
			if message.Role != "system" || !strings.HasPrefix(message.Content, prefix) {
				continue
			}
			if err = audit.inlineKnowledge(ctx, snapshot, strings.TrimPrefix(message.Content, prefix)); err != nil {
				return nil, err
			}
			roots = mergeConversationSources(roots, []agentsdk.ConversationRunReference{{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: 1}})
		}
	}
	for _, call := range snapshot.Calls {
		if ref.BeforeStep > 0 && call.Step+1 >= ref.BeforeStep {
			continue
		}
		part, e := audit.record(ctx, ref, call)
		if e != nil {
			return nil, e
		}
		roots = mergeConversationSources(roots, part)
	}
	// Public views are read before this consistent source snapshot. Completed
	// call results are immutable; later events cannot add data to that view.
	return roots, nil
}

func (audit *conversationSourceAudit) history(ctx context.Context, id string, through int64) ([]agentsdk.ConversationRunReference, error) {
	var roots []agentsdk.ConversationRunReference
	var after int64
	for page := 0; page < 256 && after < through; page++ {
		messages, err := audit.s.repo.History(ctx, id, after, through, 1000, audit.a)
		if err != nil {
			return nil, err
		}
		if len(messages) == 0 {
			return roots, nil
		}
		for _, m := range messages {
			if m.Role != "assistant" || m.RunID == "" {
				continue
			}
			part, err := audit.run(ctx, agentsdk.ConversationRunReference{ConversationID: id, RunID: m.RunID})
			if err != nil {
				return nil, err
			}
			roots = mergeConversationSources(roots, part)
		}
		after = messages[len(messages)-1].Seq
	}
	if after < through {
		return nil, conversationFailure("unavailable", "source_limit_exceeded")
	}
	return roots, nil
}

func (audit *conversationSourceAudit) inlineKnowledge(ctx context.Context, snapshot persistence.ConversationSourceSnapshot, saved string) error {
	if err := audit.connectedTool(ctx, "knowledge_search"); err != nil {
		return err
	}
	if audit.s.options.Knowledge == nil {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	messages, err := audit.s.repo.History(ctx, snapshot.Run.ConversationID, snapshot.Run.UserSeq-1, snapshot.Run.UserSeq, 1, audit.a)
	if err != nil {
		return err
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		return conversationFailure("unavailable", "source_reference_invalid")
	}
	current, err := audit.s.options.Knowledge.Search(ctx, messages[0].Content, audit.a)
	if err != nil {
		return err
	}
	canonical := func(raw []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	one, err := canonical([]byte(saved))
	if err != nil {
		return conversationFailure("unavailable", "source_reference_invalid")
	}
	two, err := canonical(current)
	if err != nil {
		return conversationFailure("unavailable", "knowledge_response_invalid")
	}
	if !bytes.Equal(one, two) {
		return conversationFailure("conflict", "knowledge_source_changed")
	}
	return nil
}

// Early history tool results carried message IDs but no run ID. Resolve that
// immutable owner-scoped message instead of treating a missing run as public.
func (audit *conversationSourceAudit) historyReference(ctx context.Context, conversationID, messageID, runID string) ([]agentsdk.ConversationRunReference, error) {
	if runID == "" {
		repo, ok := audit.s.repo.(persistence.ConversationHistoryRepository)
		if !ok {
			return nil, conversationFailure("unavailable", "source_read_unavailable")
		}
		message, err := repo.HistoryMessage(ctx, conversationID, messageID, audit.a)
		if err != nil {
			return nil, err
		}
		if message.Role != "assistant" || message.RunID == "" {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		runID = message.RunID
	}
	return audit.run(ctx, agentsdk.ConversationRunReference{ConversationID: conversationID, RunID: runID})
}

func (audit *conversationSourceAudit) connectedTool(ctx context.Context, key string) error {
	ready, err := audit.s.conversationToolAvailable(ctx, audit.a, key)
	if err != nil {
		return err
	}
	if !ready {
		return conversationFailure("unavailable", "tool_unavailable")
	}
	return nil
}

func (audit *conversationSourceAudit) record(ctx context.Context, owner agentsdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]agentsdk.ConversationRunReference, error) {
	if record.Result == nil || record.Result.Status != "completed" {
		return nil, nil
	}
	// A user may disable any tool, including local/personal tools. Reuse of its
	// stored output must honor the same live policy as a new invocation.
	if err := audit.connectedTool(ctx, record.Call.Name); err != nil {
		return nil, err
	}
	key := conversationDigest([]any{owner, record.Step, record.Call.ID})
	if audit.records[key] {
		return nil, conversationFailure("unavailable", "source_reference_invalid")
	}
	audit.records[key] = true
	defer delete(audit.records, key)
	if privateAttachmentCall(record.Call) || record.Result != nil && retiredDocumentResult(*record.Result) {
		return nil, conversationFailure("forbidden", "knowledge_source_retired")
	}
	if privateRemoteAttachmentCall(record.Call) {
		if audit.conversationID != "" && audit.conversationID != owner.ConversationID {
			return nil, conversationFailure("forbidden", "attachment_conversation_mismatch")
		}
		request := agentsdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
		if err := audit.s.authorizeStoredToolResult(ctx, request, *record.Result); err != nil {
			return nil, err
		}
		return []agentsdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, nil
	}
	if definition, business := businessTool(record.Call.Name); business {
		if conversationDigest(definition) != conversationDigest(record.Definition) {
			return nil, conversationFailure("conflict", "tool_changed")
		}
		if audit.s.options.Business == nil || audit.s.options.ToolHost == nil {
			return nil, conversationFailure("forbidden", "business_access_denied")
		}
		request := agentsdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
		auth, err := audit.s.options.ToolHost.AuthorizeConversationTool(ctx, request)
		if err != nil {
			return nil, err
		}
		if !auth.Granted || auth.ConfirmationRequired && definition.Effect != "write" {
			return nil, conversationFailure("forbidden", "business_access_denied")
		}
		if err := audit.s.authorizeStoredToolResult(ctx, request, *record.Result); err != nil {
			return nil, err
		}
		return []agentsdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, nil
	}
	if _, knowledge := knowledgeTool(record.Call.Name); knowledge {
		if audit.conversationID != "" && audit.conversationID != owner.ConversationID && privateAttachmentCall(record.Call) {
			return nil, conversationFailure("forbidden", "attachment_conversation_mismatch")
		}
		if !knownKnowledgeDefinition(record.Definition) {
			return nil, conversationFailure("conflict", "tool_changed")
		}
		policy := audit.s.options.PersonalAuthorizer
		if policy == nil && audit.s.options.ToolHost != nil {
			policy = audit.s.options.ToolHost
		}
		source, ok := audit.s.options.Knowledge.(agentsdk.ConversationKnowledgeSource)
		if policy == nil || !ok {
			return nil, conversationFailure("forbidden", "knowledge_access_denied")
		}
		request := agentsdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
		auth, err := policy.AuthorizeConversationTool(ctx, request)
		if err != nil {
			return nil, err
		}
		if !auth.Granted {
			return nil, conversationFailure("forbidden", "knowledge_access_denied")
		}
		host := knowledgeConversationHost{source: source}
		if err = host.AuthorizeConversationToolResult(ctx, request, *record.Result); err != nil {
			return nil, err
		}
		return []agentsdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, nil
	}
	if _, ok := artifactTool(record.Call.Name); ok {
		return audit.artifactToolRecord(ctx, owner, record)
	}
	// Product-owned tools remain subject to current authorization when their
	// saved results are reused in history, summaries or generated documents.
	personal := false
	for _, d := range agentsdk.PersonalConversationTools() {
		personal = personal || d.Key == record.Call.Name
	}
	if !personal && (audit.s.profile != nil || len(audit.s.options.ToolDefinitions) > 0) {
		if audit.s.options.ToolHost == nil {
			return nil, conversationFailure("forbidden", "tool_access_denied")
		}
		request := agentsdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
		auth, err := audit.s.options.ToolHost.AuthorizeConversationTool(ctx, request)
		if err != nil {
			return nil, err
		}
		if !auth.Granted {
			return nil, conversationFailure("forbidden", "tool_access_denied")
		}
		if err = audit.s.authorizeStoredToolResult(ctx, request, *record.Result); err != nil {
			return nil, err
		}
		return []agentsdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, nil
	}
	switch record.Call.Name {
	case "history_search":
		var page agentsdk.ConversationHistorySearchResult
		if json.Unmarshal(record.Result.Content, &page) != nil {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		var roots []agentsdk.ConversationRunReference
		for _, hit := range page.Items {
			if hit.Role == "assistant" {
				part, err := audit.historyReference(ctx, hit.ConversationID, hit.MessageID, hit.RunID)
				if err != nil {
					return nil, err
				}
				roots = mergeConversationSources(roots, part)
			}
		}
		return roots, nil
	case "history_read":
		var result struct {
			ConversationID string `json:"conversation_id"`
			MessageID      string `json:"message_id"`
			RunID          string `json:"run_id"`
			Role           string `json:"role"`
		}
		if json.Unmarshal(record.Result.Content, &result) != nil {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		if result.Role == "assistant" {
			return audit.historyReference(ctx, result.ConversationID, result.MessageID, result.RunID)
		}
	case "tool_result_read":
		var args agentsdk.ConversationResultRead
		if json.Unmarshal([]byte(record.Call.Arguments), &args) != nil {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		ref := agentsdk.ConversationRunReference{ConversationID: args.Reference.ConversationID, RunID: args.Reference.RunID}
		repo, ok := audit.s.repo.(persistence.ConversationExecutionReadRepository)
		if !ok {
			return nil, conversationFailure("unavailable", "source_read_unavailable")
		}
		original, err := repo.ReadExecutionCall(ctx, ref.ConversationID, ref.RunID, args.Reference.Step, args.Reference.CallID, audit.a)
		if err != nil {
			return nil, err
		}
		if original.Result == nil || conversationDigest(original.Result) != args.Reference.SHA256 {
			return nil, conversationFailure("conflict", "source_reference_invalid")
		}
		if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID {
			ref.BeforeStep = owner.BeforeStep
			if ref.BeforeStep > 0 && original.Step+1 >= ref.BeforeStep {
				return nil, conversationFailure("conflict", "source_reference_invalid")
			}
		}
		return audit.record(ctx, ref, original)
	case "execution_read":
		var page agentsdk.ConversationExecutionReadResult
		if json.Unmarshal(record.Result.Content, &page) != nil {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		repo, ok := audit.s.repo.(persistence.ConversationExecutionReadRepository)
		if !ok || len(page.Items) > 5 {
			return nil, conversationFailure("unavailable", "source_reference_invalid")
		}
		var roots []agentsdk.ConversationRunReference
		for _, item := range page.Items {
			original, err := repo.ReadExecutionCall(ctx, page.ConversationID, page.RunID, item.Step, item.CallID, audit.a)
			if err != nil {
				return nil, err
			}
			if conversationRecordHash(original) != item.RecordHash {
				return nil, conversationFailure("conflict", "source_reference_invalid")
			}
			ref := agentsdk.ConversationRunReference{ConversationID: page.ConversationID, RunID: page.RunID}
			if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID {
				ref.BeforeStep = owner.BeforeStep
				if ref.BeforeStep > 0 && original.Step+1 >= ref.BeforeStep {
					return nil, conversationFailure("conflict", "source_reference_invalid")
				}
			}
			part, err := audit.record(ctx, ref, original)
			if err != nil {
				return nil, err
			}
			roots = mergeConversationSources(roots, part)
		}
		return roots, nil
	}
	return nil, nil
}

func sourceAccessCode(err error) string {
	var coded *agentsdk.Error
	if errors.As(err, &coded) && strings.HasSuffix(coded.Code, "source_snapshot_changed") {
		return "source_snapshot_changed"
	}
	return "source_access_unavailable"
}

func (s *ConversationService) sourceAccessContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 20*time.Second)
}

func (s *ConversationService) checkRunSources(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority) error {
	if _, ok := s.repo.(persistence.ConversationSourceRepository); !ok {
		return nil
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	_, err := s.sourceAudit(a, ref.ConversationID).run(ctx, ref)
	return err
}

func (s *ConversationService) projectConversationRun(ctx context.Context, run agentsdk.ConversationRun, a agentsdk.ConversationAuthority) agentsdk.ConversationRun {
	if run.DraftText == "" && len(run.Steps) == 0 && run.Interaction == nil {
		return run
	}
	if _, ok := s.repo.(persistence.ConversationSourceRepository); !ok {
		return run
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	if _, err := s.sourceAudit(a, run.ConversationID).run(ctx, agentsdk.ConversationRunReference{ConversationID: run.ConversationID, RunID: run.ID}); err != nil {
		run.AccessError = sourceAccessCode(err)
		run.DraftText = ""
		run.DraftBytes = 0
		run.Steps = nil
		run.Interaction = nil
	}
	return run
}
