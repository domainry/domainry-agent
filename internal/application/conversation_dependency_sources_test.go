package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

type dependencySourceTestRepository struct {
	*contractPublicationTestRepository
	upstream sdk.ConversationDelegation
	contract persistence.ConversationContractPublicationRecord
	release  persistence.ConversationSourceRelease
}

type multipleDependencyOwnerRepository struct {
	*contractPublicationTestRepository
	upstreams map[string]sdk.ConversationDelegation
	contracts map[string]persistence.ConversationContractPublicationRecord
	releases  map[string]persistence.ConversationSourceRelease
}

type multipleOwnerSourceReadRepository struct {
	*multipleDependencyOwnerRepository
	run     sdk.ConversationRun
	records map[string]persistence.ConversationToolExecution
}

func (r *multipleOwnerSourceReadRepository) Run(_ context.Context, conversation, run string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	if conversation != r.run.ConversationID || run != r.run.ID || a.UserID != r.executor.UserID {
		return sdk.ConversationRun{}, conversationFailure("not_found", "run_not_found")
	}
	return r.run, nil
}

func (r *multipleOwnerSourceReadRepository) ConversationResult(_ context.Context, ref sdk.ConversationResultReference, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	if r.snapshots[ref.RunID].Authority != a {
		return persistence.ConversationToolExecution{}, conversationFailure("forbidden", "original_proof_identity_changed")
	}
	return r.records[ref.RunID], nil
}

func (r *multipleDependencyOwnerRepository) ConversationDelegation(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if d, found := r.upstreams[id]; found {
		return d, nil
	}
	return r.contractPublicationTestRepository.ConversationDelegation(ctx, id, a)
}

func (r *multipleDependencyOwnerRepository) ConversationContractPublicationRecord(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	if record, found := r.contracts[id]; found {
		if revision != record.Agreement.Revision {
			return persistence.ConversationContractPublicationRecord{}, conversationFailure("not_found", "agreement_not_found")
		}
		return record, nil
	}
	return r.contractPublicationTestRepository.ConversationContractPublicationRecord(ctx, id, revision, a)
}

func (r *multipleDependencyOwnerRepository) ConversationSourceReleases(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	if release, found := r.releases[ref.RunID]; found {
		return []persistence.ConversationSourceRelease{release}, nil
	}
	return r.contractPublicationTestRepository.ConversationSourceReleases(ctx, ref, a)
}

func TestAdmittedDependencyGraphUsesOriginalVersionsAndExactDuplicateEvidence(t *testing.T) {
	s, base, _, _, reader := contractPublicationServiceFixture()
	a := sdk.ConversationTaskDependency{ConversationDependencyInput: sdk.ConversationDependencyInput{DelegationID: "graph-a", BriefVersion: 1, AgreementRevision: 1}, Digest: "original-a", Values: json.RawMessage(`{"goal":"A"}`)}
	b := sdk.ConversationTaskDependency{ConversationDependencyInput: sdk.ConversationDependencyInput{DelegationID: "graph-b", BriefVersion: 1, AgreementRevision: 1, Fields: []string{}}, Digest: "original-b", Values: json.RawMessage(`{"goal":"B"}`)}
	r := &multipleDependencyOwnerRepository{contractPublicationTestRepository: base, contracts: map[string]persistence.ConversationContractPublicationRecord{
		a.DelegationID: {Agreement: sdk.ConversationAgreementRevision{Revision: 1, Brief: sdk.ConversationTaskBrief{Version: 1}}},
		b.DelegationID: {Agreement: sdk.ConversationAgreementRevision{Revision: 1, Brief: sdk.ConversationTaskBrief{Version: 1}, Dependencies: []sdk.ConversationTaskDependency{a}}},
	}, upstreams: map[string]sdk.ConversationDelegation{
		b.DelegationID: {ID: b.DelegationID, AgreementRevision: 2, Dependencies: []sdk.ConversationTaskDependency{{ConversationDependencyInput: sdk.ConversationDependencyInput{DelegationID: "unadopted-later-dependency"}}}},
	}}
	s.repo = r
	expected, err := s.admittedContractDependencies(t.Context(), []sdk.ConversationTaskDependency{b}, reader)
	if err != nil || conversationDigest(expected) != conversationDigest([]sdk.ConversationTaskDependency{b, a}) {
		t.Fatal("frozen graph followed today's upstream instead of its adopted version", expected, err)
	}
	duplicate := a
	duplicate.Fields = []string{}
	frozen, err := canonicalFrozenDependencies([]sdk.ConversationTaskDependency{b, a, duplicate})
	if err != nil || conversationDigest(frozen) != conversationDigest(expected) {
		t.Fatal("exact legacy duplicates changed the adopted graph", frozen, err)
	}
	duplicate.Digest = "substituted-proof"
	if _, err := canonicalFrozenDependencies([]sdk.ConversationTaskDependency{b, a, duplicate}); !collaborationDenied(err) {
		t.Fatal("conflicting duplicate evidence entered the frozen graph", err)
	}
	bad := b
	bad.AgreementRevision = 2
	if _, err := s.admittedContractDependencies(t.Context(), []sdk.ConversationTaskDependency{bad}, reader); !collaborationDenied(err) {
		t.Fatal("unknown adopted agreement silently used a known version", err)
	}
	nilGraph, err := canonicalFrozenDependencies(nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyGraph, err := canonicalFrozenDependencies([]sdk.ConversationTaskDependency{})
	if err != nil {
		t.Fatal(err)
	}
	if conversationDigest(nilGraph) != conversationDigest(emptyGraph) {
		t.Fatal("empty dependency graphs changed after storage round-trip")
	}
}

func TestMultipleDependencyOwnersKeepIndependentOriginalPublishersAndRecipientScopes(t *testing.T) {
	s, base, policy, issuer, _ := contractPublicationServiceFixture()
	r := &multipleDependencyOwnerRepository{contractPublicationTestRepository: base,
		upstreams: map[string]sdk.ConversationDelegation{},
		contracts: map[string]persistence.ConversationContractPublicationRecord{},
		releases:  map[string]persistence.ConversationSourceRelease{},
	}
	s.repo = r
	reader := base.executor
	var edges []sdk.ConversationTaskDependency
	for _, id := range []string{"owner-a", "owner-b"} {
		publisher := issuer
		publisher.UserID, publisher.RoleKey = id, id+"-publisher"
		producer := publisher
		producer.RoleKey = id + "-original"
		root := sdk.ConversationRunReference{ConversationID: id + "-private", RunID: id + "-run", BeforeStep: 2}
		brief := sdk.ConversationTaskBrief{Version: 1, Goal: id + " original requirements"}
		r.upstreams[id] = sdk.ConversationDelegation{ID: id, OwnerUserID: id, Brief: brief, AgreementRevision: 1,
			Participants: []sdk.ConversationDelegationParticipant{
				{UserID: issuer.UserID, Publisher: &publisher, Revision: 1, Operations: []string{"view"}},
				{UserID: reader.UserID, Publisher: &publisher, Revision: 1, Operations: []string{"view"}},
			}}
		r.contracts[id] = persistence.ConversationContractPublicationRecord{Agreement: sdk.ConversationAgreementRevision{Revision: 1, Brief: brief, Source: &root}}
		r.releases[root.RunID] = persistence.ConversationSourceRelease{DelegationID: id, Purpose: "contract", Reference: root, Producer: producer, Publisher: &publisher}
		r.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: producer}
		values, err := execution.DependencyProjection(brief, nil, []string{"goal"})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, sdk.ConversationTaskDependency{ConversationDependencyInput: sdk.ConversationDependencyInput{DelegationID: id, BriefVersion: 1, AgreementRevision: 1, Fields: []string{"goal"}}, Source: &root, Values: values, Digest: conversationDigest(values)})
	}
	base.original.Agreement.Dependencies = edges
	preview := func() error {
		t.Helper()
		out, err := s.PreviewConversationContractPublication(t.Context(), base.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, issuer)
		if err == nil && (len(out.Sources) != 1 || out.Sources[0] != base.original.Requirements.Sources[0]) {
			t.Fatal("downstream publication took over another owner's original roots", out.Sources)
		}
		return err
	}
	if err := preview(); err != nil {
		t.Fatal("two independently shared dependency owners were not admitted", err)
	}
	for _, edge := range edges {
		original := r.snapshots[edge.Source.RunID].Authority
		if reads := r.reads[edge.Source.RunID]; len(reads) == 0 || reads[len(reads)-1] != original {
			t.Fatal("one owner's original proof was read under another identity", edge.DelegationID, reads)
		}
	}
	readRepo := &multipleOwnerSourceReadRepository{multipleDependencyOwnerRepository: r, records: map[string]persistence.ConversationToolExecution{}}
	r.d.ConversationID, r.d.TaskID, r.d.Status = "downstream-execution", "downstream-task", "running"
	readRepo.run = sdk.ConversationRun{ID: "downstream-run", ConversationID: r.d.ConversationID, BackgroundTask: &sdk.ConversationTaskExecution{DelegationID: r.d.ID, TaskID: r.d.TaskID, BriefVersion: 1, AgreementRevision: 1, Requirements: r.original.Requirements, Dependencies: append([]sdk.ConversationTaskDependency(nil), edges...)}}
	r.snapshots[readRepo.run.ID] = persistence.ConversationSourceSnapshot{Authority: reader}
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", ActionKey: "specialist.execute", TimeoutMillis: 1000}
	host := &deliveryReadTestHost{}
	s.options.ContextBytes = 32768
	s.options.ToolHost, s.options.ToolDefinitions, s.repo = host, []sdk.ConversationToolDefinition{definition}, readRepo
	var requests []sdk.ConversationDelegationSourceRead
	request := sdk.ConversationToolRequest{Authority: reader, ConversationID: readRepo.run.ConversationID, RunID: readRepo.run.ID}
	for _, edge := range edges {
		record := r.contracts[edge.DelegationID]
		record.Requirements.Sources = []sdk.ConversationRunReference{*edge.Source}
		r.contracts[edge.DelegationID] = record
		original := persistence.ConversationToolExecution{State: "completed", Step: 0, Definition: definition, Call: sdk.ConversationToolCall{ID: "original", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"total":70}`)}}
		readRepo.records[edge.Source.RunID] = original
		args := sdk.ConversationDelegationSourceRead{ID: r.d.ID, DependencyID: edge.DelegationID, ConversationResultRead: sdk.ConversationResultRead{Reference: sdk.ConversationResultReference{ConversationID: edge.Source.ConversationID, RunID: edge.Source.RunID, Step: 0, CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}, MaxBytes: 8192}}
		requests = append(requests, args)
		page, err := s.readDelegationSource(t.Context(), request, args)
		if err != nil || !page.Complete || page.DependencyID != args.DependencyID || host.executionChecks != 0 {
			t.Fatal("exact dependency source was unavailable or gained invocation authority", page, err)
		}
		current, _ := collaborationTool("delegation_source_read")
		argJSON, _ := json.Marshal(args)
		pageJSON, _ := json.Marshal(page)
		wrapper := persistence.ConversationToolExecution{State: "completed", Definition: current, Call: sdk.ConversationToolCall{ID: "page", Name: current.Key, Arguments: string(argJSON)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: r.d.ID, Content: pageJSON}}
		owner := sdk.ConversationRunReference{ConversationID: readRepo.run.ConversationID, RunID: readRepo.run.ID}
		if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err != nil {
			t.Fatal("saved dependency page lost its original namespace", err)
		}
		page.DependencyID = "other-owner"
		wrapper.Result.Content, _ = json.Marshal(page)
		if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err == nil {
			t.Fatal("saved page's dependency identity was substituted")
		}
		args.DependencyID = ""
		if _, err := s.readDelegationSource(t.Context(), request, args); !collaborationDenied(err) {
			t.Fatal("foreign dependency root became a direct downstream source", err)
		}
	}
	badRequest := requests[0]
	badRequest.DependencyID = requests[1].DependencyID
	if _, err := s.readDelegationSource(t.Context(), request, badRequest); !collaborationDenied(err) {
		t.Fatal("another dependency's root entered the selected upstream namespace", err)
	}
	readRepo.run.BackgroundTask.Dependencies[0].Digest = "changed-frozen-dependency"
	if _, err := s.readDelegationSource(t.Context(), request, requests[1]); !collaborationDenied(err) {
		t.Fatal("changed execution dependency snapshot bypassed original agreement", err)
	}
	readRepo.run.BackgroundTask.Dependencies[0] = edges[0]
	for i, edge := range edges {
		for _, role := range []string{edge.DelegationID + "-original", edge.DelegationID + "-publisher"} {
			policy.deniedRole = role
			if err := preview(); !collaborationDenied(err) {
				t.Fatal("one owner's role withdrawal did not stop combined dependency publication", role, err)
			}
			if _, err := s.sourceAudit(reader).dependency(t.Context(), edges[1-i]); err != nil {
				t.Fatal("independent upstream lost its own verified publication", role, err)
			}
			if _, err := s.readDelegationSource(t.Context(), request, requests[i]); !collaborationDenied(err) {
				t.Fatal("dependency page ignored original or publisher withdrawal", role, err)
			}
			if _, err := s.readDelegationSource(t.Context(), request, requests[1-i]); err != nil {
				t.Fatal("independent dependency page used the other owner's withdrawn role", role, err)
			}
		}
	}
	policy.deniedRole = "revoked"
	upstream := r.upstreams[edges[0].DelegationID]
	upstream.Participants = upstream.Participants[:1]
	r.upstreams[upstream.ID] = upstream
	if err := preview(); !collaborationDenied(err) {
		t.Fatal("issuer's access replaced recipient's withdrawn upstream scope", err)
	}
	if _, err := s.sourceAudit(reader).dependency(t.Context(), edges[1]); err != nil {
		t.Fatal("other owner's audience was confused with withdrawn audience", err)
	}
	bad := edges[1]
	bad.Source = edges[0].Source
	if _, err := s.sourceAudit(reader).dependency(t.Context(), bad); !collaborationDenied(err) {
		t.Fatal("another owner's root was substituted into the admitted dependency", err)
	}
}

func (r *dependencySourceTestRepository) ConversationDelegation(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if id == r.upstream.ID {
		return r.upstream, nil
	}
	return r.contractPublicationTestRepository.ConversationDelegation(ctx, id, a)
}

func (r *dependencySourceTestRepository) ConversationContractPublicationRecord(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	if id == r.upstream.ID {
		if revision != r.contract.Agreement.Revision {
			return persistence.ConversationContractPublicationRecord{}, conversationFailure("not_found", "agreement_not_found")
		}
		return r.contract, nil
	}
	return r.contractPublicationTestRepository.ConversationContractPublicationRecord(ctx, id, revision, a)
}

func (r *dependencySourceTestRepository) ConversationSourceReleases(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	if ref.RunID == r.release.Reference.RunID {
		return []persistence.ConversationSourceRelease{r.release}, nil
	}
	return r.contractPublicationTestRepository.ConversationSourceReleases(ctx, ref, a)
}

func TestDependencySourceUsesOriginalAgreementPublisherAndCurrentAudience(t *testing.T) {
	s, base, policy, issuer, _ := contractPublicationServiceFixture()
	reader := base.executor
	reader.RoleKey = "dependency-reader"
	publisher, producer := issuer, issuer
	publisher.UserID, publisher.RoleKey = "upstream-owner", "upstream-publisher"
	producer.UserID, producer.RoleKey = publisher.UserID, "upstream-original-role"
	root := sdk.ConversationRunReference{ConversationID: "upstream-private", RunID: "upstream-old-run", BeforeStep: 1}
	old := sdk.ConversationTaskBrief{Version: 1, Goal: "Original scope", Deliverable: "Original findings"}
	r := &dependencySourceTestRepository{contractPublicationTestRepository: base,
		upstream: sdk.ConversationDelegation{ID: "upstream", OwnerUserID: publisher.UserID, AgreementRevision: 2, Brief: sdk.ConversationTaskBrief{Version: 2, Goal: "Current changed scope"}},
		contract: persistence.ConversationContractPublicationRecord{Agreement: sdk.ConversationAgreementRevision{Revision: 1, Brief: old, Source: &root}},
		release:  persistence.ConversationSourceRelease{DelegationID: "upstream", Purpose: "contract", Reference: root, Producer: producer, Publisher: &publisher},
	}
	for _, user := range []string{issuer.UserID, reader.UserID} {
		r.upstream.Participants = append(r.upstream.Participants, sdk.ConversationDelegationParticipant{UserID: user, Publisher: &publisher, Revision: 1, Operations: []string{"view"}})
	}
	r.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: producer}
	s.repo = r
	values, err := execution.DependencyProjection(old, nil, []string{"goal"})
	if err != nil {
		t.Fatal(err)
	}
	edge := sdk.ConversationTaskDependency{ConversationDependencyInput: sdk.ConversationDependencyInput{DelegationID: r.upstream.ID, AgreementRevision: 1, BriefVersion: 1, Fields: []string{"goal"}}, Values: values, Digest: conversationDigest(values), Source: &root}
	if _, err = s.sourceAudit(reader).dependency(t.Context(), edge); err != nil {
		t.Fatal("original dependency replaced by today's agreement or caller ownership", err)
	}
	if reads := r.reads[root.RunID]; len(reads) == 0 || reads[len(reads)-1] != producer {
		t.Fatal("original immutable evidence identity changed", reads)
	}
	for _, role := range []string{reader.RoleKey, publisher.RoleKey, producer.RoleKey} {
		policy.deniedRole = role
		if _, err = s.sourceAudit(reader).dependency(t.Context(), edge); !collaborationDenied(err) {
			t.Fatal("dependency ignored current role withdrawal", role, err)
		}
	}
	policy.deniedRole = "revoked"
	for _, change := range []func(*sdk.ConversationTaskDependency){
		func(e *sdk.ConversationTaskDependency) { e.Values = json.RawMessage(`{"goal":"Invented scope"}`) },
		func(e *sdk.ConversationTaskDependency) { e.Values = nil; e.Digest = "unverified" },
		func(e *sdk.ConversationTaskDependency) { e.Source = nil },
		func(e *sdk.ConversationTaskDependency) { e.Fields = []string{"deliverable"} },
		func(e *sdk.ConversationTaskDependency) { e.AgreementRevision = 2 },
	} {
		bad := edge
		change(&bad)
		if _, err = s.sourceAudit(reader).dependency(t.Context(), bad); !collaborationDenied(err) {
			t.Fatal("invented or unverifiable original dependency accepted", bad, err)
		}
	}
	for _, purpose := range []string{"delivery", "execution"} {
		r.release.Purpose = purpose
		if _, err = s.sourceAudit(reader).dependency(t.Context(), edge); !collaborationDenied(err) {
			t.Fatal("another publication purpose released dependency source", purpose, err)
		}
	}
	r.release.Purpose = "contract"
	base.original.Agreement.Dependencies = []sdk.ConversationTaskDependency{edge}
	preview, err := s.PreviewConversationContractPublication(t.Context(), base.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, issuer)
	if err != nil || len(preview.Sources) != 1 || preview.Sources[0] == root {
		t.Fatal("downstream publication claimed the upstream private root", preview, err)
	}
	manager := reader
	manager.UserID, manager.RoleKey = "participant-manager", "participant-manager-role"
	r.upstream.Participants = append(r.upstream.Participants, sdk.ConversationDelegationParticipant{UserID: manager.UserID, Publisher: &publisher, Revision: 1, Operations: []string{"view"}})
	base.d.Participants = []sdk.ConversationDelegationParticipant{{UserID: manager.UserID, Publisher: &issuer, Revision: 1, Operations: []string{"view", "manage"}}}
	base.d.RootConversationID = "opaque-goal"
	base.d.Dependencies = []sdk.ConversationTaskDependency{edge}
	base.d.Requirements = base.original.Requirements
	base.d.AgreementRevision = 1
	shared, err := s.projectConversationDelegation(t.Context(), base.d, manager)
	if err != nil || shared.ContractOmitted || len(shared.Dependencies) != 1 || len(shared.DependencyStates) != 1 || shared.DependencyStates[0].State != "changed" || shared.RootConversationID != base.d.RootConversationID || shared.Task != nil || shared.TaskID != "" || shared.SourceConversationID != "" || shared.ConversationID != "" {
		t.Fatal("participant dependency management lost exact edges or inherited private control", shared, err)
	}
	r.upstream.Participants = r.upstream.Participants[:1]
	if _, err = s.sourceAudit(reader).dependency(t.Context(), edge); !collaborationDenied(err) {
		t.Fatal("withdrawn upstream audience survived original snapshot", err)
	}
	if _, err = s.PreviewConversationContractPublication(t.Context(), base.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, issuer); !collaborationDenied(err) {
		t.Fatal("downstream republication implicitly restored recipient upstream access", err)
	}
	shared, err = s.projectConversationDelegation(t.Context(), base.d, manager)
	if err != nil || !shared.ContractOmitted || len(shared.Dependencies) != 0 || len(shared.DependencyStates) != 0 || shared.RootConversationID != "" {
		t.Fatal("withdrawn upstream audience remained in participant projection", shared, err)
	}
}
