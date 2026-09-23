package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

func normalizeAgreement(d *sdk.ConversationDelegation) {
	if d.Dependencies == nil {
		d.Dependencies = []sdk.ConversationTaskDependency{}
	}
	if d.AgreementRevision == 0 {
		d.AgreementRevision = 1
	}
	if d.Delivery != nil && d.Delivery.AgreementRevision == 0 && d.Delivery.BriefVersion == d.Brief.Version {
		copy := *d.Delivery
		copy.AgreementRevision = 1
		d.Delivery = &copy
	}
}

func (s *ConversationStore) freezeTaskDependencies(ctx context.Context, tx *sql.Tx, id, root string, refs []sdk.ConversationDependencyInput, a sdk.ConversationAuthority) ([]sdk.ConversationTaskDependency, error) {
	if len(refs) > 16 {
		return nil, conversationError("bad_request", "dependencies_invalid")
	}
	result := []sdk.ConversationTaskDependency{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.AgreementRevision < 0 || !personalMemoryKey(ref.DelegationID) || ref.DelegationID == id || seen[ref.DelegationID] {
			return nil, conversationError("bad_request", "dependencies_invalid")
		}
		seen[ref.DelegationID] = true
		upstream, err := s.dependencyDelegation(ctx, tx, ref.DelegationID, a)
		if err != nil {
			return nil, err
		}
		if upstream.RootConversationID != root {
			return nil, conversationError("forbidden", "dependency_goal_mismatch")
		}
		if upstream.Brief.Version != ref.BriefVersion || max(1, ref.AgreementRevision) != upstream.AgreementRevision {
			return nil, conversationError("conflict", "dependency_version_changed")
		}
		for _, change := range upstream.PendingChanges {
			if change.SourceDelegationID != upstream.ID {
				return nil, conversationError("conflict", "dependency_update_required")
			}
		}
		values, err := execution.DependencyProjection(upstream.Brief, upstream.StructuredInput, ref.Fields)
		if err != nil {
			return nil, conversationError("bad_request", "dependencies_invalid")
		}
		ref.AgreementRevision = upstream.AgreementRevision
		ref.Fields = append([]string{}, ref.Fields...)
		sort.Strings(ref.Fields)
		var inputSource *sdk.ConversationRunReference
		if execution.DependencyUsesInput(ref.Fields) {
			inputSource = upstream.InputSource
		}
		result = append(result, sdk.ConversationTaskDependency{InputSource: inputSource, ConversationDependencyInput: ref, Digest: conversationHash(values), Values: values, Source: upstream.BriefSource})
		// A bounded dependency graph is separate from the provenance chain used
		// for Agent recursion limits. Both kinds of cycle are rejected.
		queue := append([]sdk.ConversationTaskDependency{}, upstream.Dependencies...)
		visited := map[string]bool{}
		for len(queue) > 0 {
			edge := queue[0]
			queue = queue[1:]
			if edge.DelegationID == id {
				return nil, conversationError("conflict", "dependency_cycle")
			}
			if visited[edge.DelegationID] {
				continue
			}
			visited[edge.DelegationID] = true
			if len(visited) > 32 {
				return nil, conversationError("conflict", "dependency_cycle")
			}
			next, err := s.dependencyDelegation(ctx, tx, edge.DelegationID, a)
			if err != nil {
				return nil, err
			}
			queue = append(queue, next.Dependencies...)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DelegationID < result[j].DelegationID })
	return result, nil
}

func (s *ConversationStore) saveAgreementRevision(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, fields []string, reason string, actor *sdk.ConversationToolRequest, a sdk.ConversationAuthority) error {
	entry := sdk.ConversationAgreementRevision{StructuredInput: d.StructuredInput, InputSource: d.InputSource, Revision: d.AgreementRevision, Brief: d.Brief, Dependencies: d.Dependencies, ChangedFields: fields, Reason: reason, Source: d.BriefSource, CreatedAt: time.Now().UTC()}
	requirements := d.Requirements
	entry.Requirements = &requirements
	if actor == nil {
		entry.FromUserID = a.UserID
	} else {
		entry.ChangeSource = &sdk.ConversationRunReference{ConversationID: actor.ConversationID, RunID: actor.RunID, BeforeStep: actor.Step + 1}
		c, err := s.get(ctx, tx, actor.ConversationID, a)
		if err != nil {
			return err
		}
		entry.FromAgentID = c.AgentID
		if entry.FromAgentID == "" {
			entry.FromAgentID = "default"
		}
	}
	return s.insertDelegationHistory(ctx, tx, conversationItemDelegationAgreement, d, "", entry.Revision, entry.ChangeSource, entry, a)
}

func (s *ConversationStore) ConversationAgreementHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationAgreementHistory, error) {
	out := sdk.ConversationAgreementHistory{Items: []sdk.ConversationAgreementRevision{}, Complete: true}
	current, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	if before < 0 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	p := delegationHistoryPredicate(conversationOwner(delegationRecordAuthority(current, a)), conversationItemDelegationAgreement, id, "")
	if before > 0 {
		p = query.And(p, query.LessThan("seq", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(p).OrderBy(query.Descending("seq")).Limit(21).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item sdk.ConversationAgreementRevision
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(out.Items) > 20 {
		out.Complete = false
		out.Items = out.Items[:20]
		out.NextBefore = out.Items[len(out.Items)-1].Revision
	}
	return out, rows.Err()
}

func (s *ConversationStore) propagateRequirementChange(ctx context.Context, tx *sql.Tx, origin sdk.ConversationDelegation, fields []string, a sdk.ConversationAuthority) error {
	// This is system invalidation within the already authorized goal. Record
	// ownership routes the graph; notices retain the actual changing actor.
	items, err := s.dependencyGoalDelegations(ctx, tx, origin.RootConversationID, a)
	if err != nil {
		return err
	}
	changed := map[string]bool{origin.ID: true}
	queue := []string{origin.ID}
	for len(queue) > 0 {
		via := queue[0]
		queue = queue[1:]
		for index := range items {
			d := &items[index]
			if changed[d.ID] || d.Status == "cancelled" || d.Status == "rejected" {
				continue
			}
			affected := false
			for _, edge := range d.Dependencies {
				if edge.DelegationID != via {
					continue
				}
				if via != origin.ID {
					affected = true
					break
				}
				values, err := execution.DependencyProjection(origin.Brief, origin.StructuredInput, edge.Fields)
				if err != nil {
					return err
				}
				if origin.SubjectExited || conversationHash(values) != edge.Digest || len(fields) == 1 && fields[0] == "dependencies" {
					affected = true
					break
				}
			}
			if !affected {
				continue
			}
			changed[d.ID] = true
			queue = append(queue, d.ID)
			change := sdk.ConversationRequirementChange{ID: "change_" + conversationHash([]any{origin.ID, origin.AgreementRevision, d.ID})[:32], SourceDelegationID: origin.ID, SourceBriefVersion: origin.Brief.Version, SourceAgreementRevision: origin.AgreementRevision, ViaDelegationID: via, ChangedFields: fields, CreatedAt: origin.UpdatedAt}
			pending := []sdk.ConversationRequirementChange{}
			for _, old := range d.PendingChanges {
				if old.SourceDelegationID != origin.ID {
					pending = append(pending, old)
				} else {
					change.ChangedFields = mergeRequirementFields(old.ChangedFields, change.ChangedFields)
				}
			}
			d.PendingChanges = append(pending, change)
			previous := d.Revision
			d.Revision++
			d.Status = "needs_update"
			d.UpdatedAt = origin.UpdatedAt
			if err = s.saveConversationDelegation(ctx, tx, *d, previous, delegationRecordAuthority(*d, a)); err != nil {
				return err
			}
			if err = s.controlDelegationTask(ctx, tx, *d, "invalidate_dependencies", a); err != nil {
				return err
			}
			if err = s.requirementChangeNotices(ctx, tx, *d, change, requirementChangeSource(origin, fields), a); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ConversationStore) requirementChangeNotices(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, change sdk.ConversationRequirementChange, source *sdk.ConversationRunReference, a sdk.ConversationAuthority) error {
	executor, err := s.delegationExecutionAuthority(ctx, tx, d, a)
	if err != nil {
		return err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(executor), d.TaskID)).Build()
	if err != nil {
		return err
	}
	task, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	for _, target := range []struct {
		conversation, agent string
		snapshot            *sdk.ConversationAgentSnapshot
	}{{d.ConversationID, d.ToAgentID, task.task.Agent}, {d.SourceConversationID, d.FromAgentID, d.SourceAgent}} {
		message := sdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]string{change.ID, target.conversation})[:32], DelegationID: d.ID, ToAgentID: target.agent, ConversationID: target.conversation, Kind: "requirements_changed", DeliveryMode: "next_step", Content: "The task agreement or a declared requirement dependency changed. Old execution has stopped. Review the recorded change, refresh dependency versions and explicitly resume before submitting a new delivery. This is not new user authorization.", BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Change: &change, Source: source, CreatedAt: change.CreatedAt}
		if err = s.insertConversationPeerNotice(ctx, tx, message, target.snapshot, a, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) flattenTaskDependencies(ctx context.Context, tx *sql.Tx, dependencies []sdk.ConversationTaskDependency, a sdk.ConversationAuthority) ([]sdk.ConversationTaskDependency, error) {
	out := []sdk.ConversationTaskDependency{}
	queue := append([]sdk.ConversationTaskDependency{}, dependencies...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		edge := queue[0]
		queue = queue[1:]
		key := conversationHash([]any{edge.DelegationID, append([]string{}, edge.Fields...)})
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 256 {
			return nil, conversationError("bad_request", "dependencies_invalid")
		}
		out = append(out, edge)
		d, err := s.dependencyDelegation(ctx, tx, edge.DelegationID, a)
		if err != nil {
			return nil, err
		}
		queue = append(queue, d.Dependencies...)
	}
	return out, nil
}

func (s *ConversationStore) resumeDependencyReferences(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, in *([]sdk.ConversationDependencyInput), a sdk.ConversationAuthority) ([]sdk.ConversationTaskDependency, error) {
	if in != nil {
		return s.freezeTaskDependencies(ctx, tx, d.ID, d.RootConversationID, *in, a)
	}
	refs := []sdk.ConversationDependencyInput{}
	for _, edge := range d.Dependencies {
		upstream, err := s.dependencyDelegation(ctx, tx, edge.DelegationID, a)
		if err != nil {
			return nil, err
		}
		values, err := execution.DependencyProjection(upstream.Brief, upstream.StructuredInput, edge.Fields)
		if err != nil {
			return nil, err
		}
		if conversationHash(values) != edge.Digest {
			return nil, conversationError("conflict", "dependency_review_required")
		}
		ref := edge.ConversationDependencyInput
		ref.BriefVersion = upstream.Brief.Version
		ref.AgreementRevision = upstream.AgreementRevision
		refs = append(refs, ref)
	}
	for _, change := range d.PendingChanges {
		if change.SourceDelegationID != d.ID {
			return nil, conversationError("conflict", "dependency_review_required")
		}
	}
	return s.freezeTaskDependencies(ctx, tx, d.ID, d.RootConversationID, refs, a)
}

func (s *ConversationStore) recordAgreementAdoption(ctx context.Context, tx *sql.Tx, run sdk.ConversationRun, a sdk.ConversationAuthority) error {
	if run.BackgroundTask == nil || run.BackgroundTask.DelegationID == "" {
		return nil
	}
	d, err := s.conversationDelegation(ctx, tx, run.BackgroundTask.DelegationID, a)
	if err != nil {
		return err
	}
	if d.TaskID != run.BackgroundTask.TaskID || d.ConversationID != run.ConversationID || max(1, run.BackgroundTask.AgreementRevision) != d.AgreementRevision || run.BackgroundTask.BriefVersion != d.Brief.Version || len(d.PendingChanges) > 0 {
		return conversationError("conflict", "delegation_superseded")
	}
	if d.AdoptedAgreementRevision == d.AgreementRevision {
		return nil
	}
	previous := d.Revision
	d.Revision++
	now := time.Now().UTC()
	d.UpdatedAt = now
	d.AdoptedAgreementRevision = d.AgreementRevision
	d.AdoptedAt = &now
	return s.saveConversationDelegation(ctx, tx, d, previous, a)
}

func requirementChangeSource(d sdk.ConversationDelegation, fields []string) *sdk.ConversationRunReference {
	if len(fields) == 1 && fields[0] == "input" {
		return d.InputSource
	}
	return d.BriefSource
}

func mergeRequirementFields(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, field := range group {
			if !seen[field] {
				seen[field] = true
				out = append(out, field)
			}
		}
	}
	sort.Strings(out)
	return out
}
