package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-knowledge/artifact"
	knowledge "github.com/domainry/domainry-knowledge/module"
	tools "github.com/domainry/domainry-tools-sdk"
)

type deliveryArtifactRepository struct {
	privatePeerSources
	persistence.ConversationArtifactRepository
	authority sdk.ConversationAuthority
	record    persistence.ConversationArtifactRecord
	export    sdk.ConversationArtifactExport
	snapshot  persistence.ConversationSourceSnapshot
	reads     int
}

func (r *deliveryArtifactRepository) ArtifactRecord(ctx context.Context, id string, version int64, a sdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	r.reads++
	if err := ctx.Err(); err != nil {
		return persistence.ConversationArtifactRecord{}, err
	}
	if a != r.authority || id != r.record.Artifact.ID || version != r.record.Artifact.Version {
		return persistence.ConversationArtifactRecord{}, conversationFailure("not_found", "artifact_not_found")
	}
	return r.record, nil
}

func (r *deliveryArtifactRepository) ArtifactExport(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationArtifactExport, error) {
	if a != r.authority || id != r.export.ID {
		return sdk.ConversationArtifactExport{}, conversationFailure("not_found", "artifact_export_not_found")
	}
	return r.export, nil
}

func (r *deliveryArtifactRepository) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	return r.snapshot, nil
}

type deliveryArtifactPolicy struct {
	denied, disabled map[string]bool
	requests         []sdk.ConversationToolRequest
	confirmation     bool
}

func (p *deliveryArtifactPolicy) AuthorizeConversationTool(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	p.requests = append(p.requests, in)
	return sdk.ConversationToolAuthorization{Granted: !p.denied[in.Call.Name], ConfirmationRequired: p.confirmation}, ctx.Err()
}

func (p *deliveryArtifactPolicy) ConversationToolAvailable(ctx context.Context, _ tools.Authority, key string) (bool, error) {
	return !p.disabled[key], ctx.Err()
}

func TestDeliveryArtifactsUseKnowledgeReadAccessAndExactSavedVersions(t *testing.T) {
	for _, key := range []string{"artifact_create", "artifact_edit", "artifact_export", "artifact_read", "artifact_list", "artifact_versions"} {
		t.Run(key, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
			content := sdk.ConversationArtifactContent{Kind: "markdown", Markdown: "已核对的原始成果正文"}
			body, hash, err := artifact.Encode(content)
			if err != nil {
				t.Fatal(err)
			}
			meta := sdk.ConversationArtifact{ID: "art_" + strings.Repeat("a", 32), Version: 2, Title: "成果", Kind: "markdown", Bytes: len(body), SHA256: hash}
			repo := &deliveryArtifactRepository{authority: a, record: persistence.ConversationArtifactRecord{Artifact: meta, Body: body, Sources: &sdk.ConversationSources{Version: 1}}}
			policy := &deliveryArtifactPolicy{denied: map[string]bool{"artifact_create": true, "artifact_edit": true, "artifact_export": true}, disabled: map[string]bool{}}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{KnowledgeFactory: knowledge.NewFactory(), PersonalAuthorizer: policy, ToolAvailability: policy, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
			// Deliberately empty producer profile: viewing the released resource
			// must not require the producer's old execution configuration.
			host := &deliveryReadTestHost{}
			s.options.ToolHost = &profileToolHost{base: host, allowed: map[string]bool{}}
			owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
			def, _ := artifactTool(key)
			args := map[string]any{"id": meta.ID, "version": meta.Version}
			var value any = map[string]any{"artifact": meta}
			switch key {
			case "artifact_create":
				args = map[string]any{"title": meta.Title, "content": content}
			case "artifact_edit":
				args = map[string]any{"id": meta.ID, "expected_version": 1, "patch": map[string]any{"text": []any{map[string]string{"find": "旧", "replace": "新"}}}}
			case "artifact_export":
				repo.export = sdk.ConversationArtifactExport{ID: "exp_original", ArtifactID: meta.ID, Version: meta.Version, Format: "markdown", Filename: "成果.md", SHA256: hash, Bytes: len(body), Downloads: 3}
				saved := repo.export
				saved.Downloads = 0
				value = map[string]any{"export": saved}
				args["format"] = "markdown"
			case "artifact_read":
				value, err = artifactReadPage(sdk.ConversationArtifactVersion{Artifact: meta, Content: content}, sdk.ConversationArtifactRead{ID: meta.ID, Version: meta.Version})
				if err != nil {
					t.Fatal(err)
				}
			case "artifact_list":
				args = map[string]any{}
				value = sdk.ConversationArtifactPage{Items: []sdk.ConversationArtifact{meta}, Complete: true}
			case "artifact_versions":
				args = map[string]any{"id": meta.ID}
				value = sdk.ConversationArtifactVersions{Items: []sdk.ConversationArtifact{meta}, Complete: true}
			}
			raw, _ := json.Marshal(value)
			result := sdk.ConversationToolResult{Status: "completed", ResourceID: meta.ID, Content: raw}
			record := persistence.ConversationToolExecution{Step: 1, Definition: def, Call: sdk.ConversationToolCall{ID: "original", Name: key, Arguments: conversationJSONText(args)}, Result: &result}
			ctx := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{})
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
				t.Fatal("raw replay bypassed original execution profile")
			}
			ctx = deliverySourceContext(ctx, "released")
			read := func() error { _, e := s.sourceAudit(a).record(ctx, owner, record); return e }
			if err := read(); err != nil {
				t.Fatal("Knowledge read grant did not release saved result", err)
			}
			if host.reads != 0 || host.executionChecks != 0 {
				t.Fatal("artifact bypassed Knowledge or checked producer execution")
			}
			found := false
			for _, request := range policy.requests {
				if request.Definition.Effect == "write" || request.Authority != a || request.Confirmation != nil || request.LeaseOwner != "" || request.Fence != 0 {
					t.Fatalf("read acquired execution authority: %+v", request)
				}
				var in sdk.ConversationArtifactRead
				_ = json.Unmarshal([]byte(request.Call.Arguments), &in)
				found = found || request.Call.Name == "artifact_read" && in.ID == meta.ID && in.Version == meta.Version
			}
			if !found {
				t.Fatal("exact resulting artifact revision was not authorized")
			}
			if key == "artifact_list" || key == "artifact_versions" {
				policy.denied[key] = true
				if err := read(); err == nil {
					t.Fatal("saved enumeration bypassed its current grant")
				}
				delete(policy.denied, key)
			}
			for _, disabled := range []string{key, "artifact_read"} {
				policy.disabled[disabled] = true
				before := repo.reads
				if err := read(); err == nil || repo.reads != before {
					t.Fatal("disabled source was read", err)
				}
				delete(policy.disabled, disabled)
			}
			policy.denied["artifact_read"] = true
			if err := read(); err == nil {
				t.Fatal("read revocation ignored")
			}
			delete(policy.denied, "artifact_read")
			policy.confirmation = true
			if err := read(); err == nil {
				t.Fatal("reading incorrectly acquired confirmation")
			}
			policy.confirmation = false
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view"}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("delivery revocation ignored", err)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
			other := a
			other.UserID = "other"
			if _, err := s.sourceAudit(other).record(ctx, owner, record); err == nil {
				t.Fatal("cross-owner artifact disclosed")
			}
			for index, change := range []func(){
				func() { repo.record.Artifact.Title = "modified" },
				func() { repo.record.Artifact.Version++ },
				func() { repo.record.Sources = nil },
				func() {
					repo.record.Sources = &sdk.ConversationSources{Version: 1, Omitted: []sdk.ConversationRunReference{{RunID: "omitted"}}}
				},
				func() { record.Definition.Version = "changed" },
				func() { record.Result.ErrorCode = "uncertain" },
				func() { record.Result.Content = append(append([]byte{}, raw...), []byte(` {"extra":"unverified"}`)...) },
			} {
				if key == "artifact_export" && index == 0 {
					// Export receipts contain the original filename, not the
					// artifact title. The filename is checked separately below.
					continue
				}
				saved, savedRecord, savedResult := repo.record, record, *record.Result
				change()
				if err := read(); err == nil {
					t.Fatal("modified or unverifiable evidence accepted")
				}
				repo.record, record, *record.Result = saved, savedRecord, savedResult
			}
			if key == "artifact_read" {
				repo.record.Body = json.RawMessage(`{"kind":"markdown","markdown":"tampered"}`)
				if err := read(); err == nil {
					t.Fatal("saved page disclosed with invalid immutable body")
				}
				repo.record.Body = body
			}
			if key == "artifact_export" {
				repo.export.Filename = "tampered.md"
				if err := read(); err == nil {
					t.Fatal("modified original export receipt accepted")
				}
				repo.export.Filename = "成果.md"
			}
			// Nested source authorization still traverses the exact current run.
			repo.record.Sources = &sdk.ConversationSources{Version: 1, Runs: []sdk.ConversationRunReference{{ConversationID: "source", RunID: "source-run"}}}
			source := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", TimeoutMillis: 1000}
			s.options.ToolDefinitions = []sdk.ConversationToolDefinition{source}
			repo.snapshot = persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{{Definition: source, Call: sdk.ConversationToolCall{ID: "source", Name: source.Key}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{}`)}}}}
			if err := read(); err != nil || host.reads == 0 {
				t.Fatal("source owner reading not checked", err)
			}
			host.err = &tools.Error{Class: "forbidden", Code: "source.revoked"}
			if err := read(); err != host.err {
				t.Fatal("current underlying source revocation ignored", err)
			}
			host.err = nil
			// Private inputs are never promoted into shareable artifact evidence.
			repo.snapshot, _ = (privatePeerSources{}).ConversationSourceSnapshot(t.Context(), owner, a)
			if err := read(); err == nil {
				t.Fatal("private attachment became readable through a released artifact")
			}
			repo.record.Sources.Runs = []sdk.ConversationRunReference{owner}
			repo.snapshot = persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{record}}
			if err := read(); err == nil || !strings.Contains(err.Error(), "source_reference_invalid") {
				t.Fatal("cyclic artifact provenance was not bounded", err)
			}
			cancelled, cancel := context.WithTimeout(ctx, time.Nanosecond)
			cancel()
			if _, err := s.sourceAudit(a).record(cancelled, owner, record); err == nil {
				t.Fatal("cancelled read allowed")
			}
		})
	}
}
