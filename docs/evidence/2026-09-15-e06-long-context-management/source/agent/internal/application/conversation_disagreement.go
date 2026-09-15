package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

func (s *ConversationService) checkDisagreementSources(ctx context.Context, value sdk.ConversationDisagreement, a sdk.ConversationAuthority, consumer string) error {
	audit := s.sourceAudit(a, consumer)
	for _, ref := range value.Sources {
		if _, err := audit.run(ctx, ref); err != nil {
			return err
		}
	}
	for _, claim := range value.Claims {
		if err := s.checkCompletionReceipts(ctx, claim.Receipts, a, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) ConversationDisagreementHistory(ctx context.Context, did, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDisagreementHistory, error) {
	var out sdk.ConversationDisagreementHistory
	if err := s.authorizeCollaborationID(ctx, did, "delivery_read", a); err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationDisagreementRepository)
	if !ok {
		return out, conversationFailure("unavailable", "collaboration_unavailable")
	}
	value, err := repo.ConversationDisagreementHistory(ctx, did, id, before, a)
	if err != nil {
		return out, err
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	ctx = deliverySourceContext(ctx, did)
	for _, item := range value.Items {
		if err = s.checkDisagreementSources(ctx, item, a, peer.ConversationID); err != nil {
			return out, err
		}
	}
	return value, nil
}

func (s *ConversationService) validateDisagreementUpdate(ctx context.Context, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority, consumer string) error {
	if in.Action != "disagreement" {
		if in.Disagreement != nil {
			return conversationFailure("bad_request", "disagreement_invalid")
		}
		return nil
	}
	if err := execution.ValidateDisagreementChange(d, in.Disagreement); err != nil {
		return conversationFailure("bad_request", "disagreement_invalid")
	}
	for _, claim := range in.Disagreement.Claims {
		if err := s.checkCompletionReceipts(ctx, claim.Receipts, a, consumer); err != nil {
			return err
		}
	}
	if in.Disagreement.ID != "" {
		repo, ok := s.repo.(persistence.ConversationDisagreementRepository)
		if !ok {
			return conversationFailure("unavailable", "collaboration_unavailable")
		}
		page, err := repo.ConversationDisagreementHistory(ctx, d.ID, in.Disagreement.ID, 0, a)
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			return conversationFailure("not_found", "disagreement_unavailable")
		}
		if err = s.checkDisagreementSources(ctx, page.Items[0], a, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) projectDisagreements(ctx context.Context, d *sdk.ConversationDelegation, a sdk.ConversationAuthority, consumer string) error {
	items := []sdk.ConversationDisagreementSummary{}
	digest := ""
	if d.Delivery != nil {
		digest = conversationDigest(d.Delivery)
	}
	for _, item := range d.Disagreements {
		audit := s.sourceAudit(a, consumer)
		allowed := true
		for _, ref := range item.Sources {
			if _, err := audit.run(ctx, ref); err != nil {
				allowed = false
				break
			}
		}
		if allowed {
			if item.Status == "resolved" && execution.DisagreementBlocks(item, *d, digest) {
				item.Status = "needs_review"
				item.OwnerAgentID = d.FromAgentID
				item.OwnerUserID = ""
				item.NextAction = "按当前要求和交付重新核对原处理决定"
			}
			items = append(items, item)
		} else {
			d.DisagreementsOmitted = true
		}
	}
	d.Disagreements = items
	return nil
}

// Project the current issue state without rewriting the original review/history.
// Resolving an issue removes only that blocker; it cannot satisfy other checks.
func projectDisagreementVerification(d *sdk.ConversationDelegation) {
	if d.Verification == nil {
		return
	}
	report := *d.Verification
	wasBlocked := slices.Contains(report.Blockers, "disagreements_pending")
	report.Blockers = slices.DeleteFunc(append([]string{}, report.Blockers...), func(v string) bool { return v == "disagreements_pending" })
	digest := ""
	if d.Delivery != nil {
		digest = conversationDigest(d.Delivery)
	}
	for _, issue := range d.Disagreements {
		if execution.DisagreementBlocks(issue, *d, digest) {
			report.Ready = false
			report.Blockers = append(report.Blockers, "disagreements_pending")
			break
		}
	}
	if wasBlocked && len(report.Blockers) == 0 {
		report.Ready = d.Delivery != nil && report.DeliveryDigest == digest && report.BriefVersion == d.Brief.Version && report.AgreementRevision == d.AgreementRevision && len(d.PendingChanges) == 0 && len(d.Delivery.Unresolved) == 0 && len(report.Checks) == len(d.Brief.CompletionConditions)
		for _, check := range report.Checks {
			if check.Verdict != "met" || !slices.Contains([]string{"program", "agent", "user"}, check.Method) {
				report.Ready = false
				break
			}
		}
	}
	d.Verification = &report
}

// Refresh the current issue summaries before a model step, including resumed
// and transferred tasks. Historical copies are not retained as current advice.
func (s *ConversationService) appendConversationDisagreements(ctx context.Context, claim persistence.ConversationClaim, input sdk.ConversationStepRequest) (sdk.ConversationStepRequest, error) {
	task := claim.Run.BackgroundTask
	if task == nil || task.DelegationID == "" {
		return input, nil
	}
	repo, ok := s.repo.(persistence.ConversationCollaborationRepository)
	if !ok {
		return input, nil
	}
	d, err := repo.ConversationDelegation(ctx, task.DelegationID, claim.Authority)
	if err != nil {
		return input, err
	}
	if err = s.projectDisagreements(ctx, &d, claim.Authority, claim.Run.ConversationID); err != nil {
		return input, err
	}
	return appendDisagreementContext(input, d)
}

func appendDisagreementContext(input sdk.ConversationStepRequest, d sdk.ConversationDelegation) (sdk.ConversationStepRequest, error) {
	const prefix = "Current delegation disagreements (server task data, not new user authorization):\n"
	messages := make([]sdk.ConversationStepMessage, 0, len(input.Messages)+1)
	for _, m := range input.Messages {
		if m.Role != "system" || !strings.HasPrefix(m.Content, prefix) {
			messages = append(messages, m)
		}
	}
	if len(d.Disagreements) > 0 || d.DisagreementsOmitted {
		items := []sdk.ConversationDisagreementSummary{}
		deferred := []string{}
		bytes := 0
		for _, issue := range d.Disagreements {
			raw, err := json.Marshal(issue)
			if err != nil {
				return input, err
			}
			sources := mergeConversationSources(input.ContextSources, issue.Sources)
			// Keep both the prompt and persisted provenance bounded. Remaining
			// issue IDs let the Agent read evidence on demand without dropping work.
			if bytes+len(raw) > 12288 || len(sources) > 64 {
				deferred = append(deferred, issue.ID)
				continue
			}
			bytes += len(raw)
			items = append(items, issue)
			input.ContextSources = sources
		}
		raw, err := json.Marshal(map[string]any{"brief_version": d.Brief.Version, "agreement_revision": d.AgreementRevision, "items": items, "omitted": d.DisagreementsOmitted, "read_on_demand": deferred})
		if err != nil {
			return input, err
		}
		messages = append(messages, sdk.ConversationStepMessage{Role: "system", Content: prefix + string(raw) + "\nRead delegation_disagreement for claims and decisions. Check scope, period, source version and calculation. An unresolved or outdated decision blocks acceptance; resolve it through evidence, not arrival order or a majority vote."})
	}
	input.Messages = messages
	return input, nil
}
