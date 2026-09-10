package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/artifactstorage"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type artifactTestPolicy struct{ denied atomic.Value }

type corruptArtifactStorage struct{ writes int }

func (s *corruptArtifactStorage) PutArtifactContent(context.Context, string, []byte, agentsdk.ConversationAuthority) (string, error) {
	s.writes++
	return "opaque-ref", nil
}
func (*corruptArtifactStorage) ReadArtifactContent(context.Context, string, agentsdk.ConversationAuthority) ([]byte, error) {
	return []byte(`{"kind":"markdown","markdown":"different content"}`), nil
}

func (p *artifactTestPolicy) AuthorizeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	denied, _ := p.denied.Load().(string)
	return agentsdk.ConversationToolAuthorization{Granted: in.Definition.Key != denied}, nil
}

func artifactErrorClass(t *testing.T, err error, class string) {
	t.Helper()
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Class != class {
		t.Fatalf("want %s, got %v", class, err)
	}
}

func TestArtifactsSaaSRoundTripVersionsReceiptsAndDownloadAuthority(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	storage, err := artifactstorage.NewFiles(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	policy := &artifactTestPolicy{}
	options := application.ConversationOptions{PersonalAuthorizer: policy, ArtifactStorage: storage}
	service, err := application.NewConversationService(repo, nil, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "artifact-saas-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "artifact-saas-fixture", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	api := binding.(agentsdk.ConversationBinding).Conversations().(agentsdk.ConversationArtifactService)
	input := agentsdk.ConversationArtifactCreate{ClientID: "report", Title: "本周周报", Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "# 周报\n\n## 第一节\n已完成。\n\n## 第二节\n待核对。\n\n" + strings.Repeat("附录资料。\n", 7000)}}
	created, err := api.CreateArtifact(t.Context(), input, a)
	if err != nil || created.Content.Markdown != input.Content.Markdown || created.Artifact.Version != 1 {
		t.Fatal("large artifact did not round trip", err)
	}
	record, err := repo.ArtifactRecord(t.Context(), created.Artifact.ID, 1, a)
	if err != nil || len(record.Body) != 0 || record.BodyRef == "" {
		t.Fatal("large content was not stored outside metadata", err)
	}
	public, _ := json.Marshal(created)
	if strings.Contains(string(public), "body_ref") || strings.Contains(string(public), record.BodyRef) {
		t.Fatal("host storage reference escaped public boundary")
	}
	edit := agentsdk.ConversationArtifactEdit{ClientID: "edit-second", ExpectedVersion: 1, Patch: agentsdk.ConversationArtifactPatch{Text: []agentsdk.ConversationArtifactTextEdit{{Find: "## 第二节\n待核对。", Replace: "## 第二节\n已核对。"}}}}
	updated, err := api.EditArtifact(t.Context(), created.Artifact.ID, edit, a)
	if err != nil || updated.Artifact.Version != 2 || !strings.Contains(updated.Content.Markdown, "已核对。") {
		t.Fatal("second section edit failed", err)
	}
	replay, err := api.EditArtifact(t.Context(), created.Artifact.ID, edit, a)
	if err != nil || string(mustJSON(updated)) != string(mustJSON(replay)) {
		t.Fatal("edit receipt was not stable", err)
	}
	// Different commands producing identical output must not share a receipt.
	changedInput := edit
	changedInput.Patch = agentsdk.ConversationArtifactPatch{Content: &updated.Content}
	_, err = api.EditArtifact(t.Context(), created.Artifact.ID, changedInput, a)
	artifactErrorClass(t, err, "conflict")
	stale := edit
	stale.ClientID = "stale-edit"
	_, err = api.EditArtifact(t.Context(), created.Artifact.ID, stale, a)
	artifactErrorClass(t, err, "conflict")
	replayedCreate, err := api.CreateArtifact(t.Context(), input, a)
	if err != nil || replayedCreate.Artifact.Version != 1 {
		t.Fatal("create replay advanced original receipt", err)
	}
	head, err := api.Artifact(t.Context(), created.Artifact.ID, 0, a)
	if err != nil || head.Artifact.Version != 2 {
		t.Fatal("replay rolled back head", err)
	}
	versions, err := api.ArtifactVersions(t.Context(), created.Artifact.ID, 0, 1, a)
	if err != nil || len(versions.Items) != 1 || versions.Complete || versions.NextBefore != 2 {
		t.Fatal("version pagination lost", err)
	}
	versions, err = api.ArtifactVersions(t.Context(), created.Artifact.ID, versions.NextBefore, 1, a)
	if err != nil || len(versions.Items) != 1 || versions.Items[0].Version != 1 || !versions.Complete {
		t.Fatal("old version page missing", err)
	}
	page, err := api.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{Query: "周报"}, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != 2 {
		t.Fatal("artifact list missing current version", err)
	}
	request := agentsdk.ConversationArtifactExportRequest{ClientID: "export-original", Version: 1, Format: "markdown"}
	exported, err := api.ExportArtifact(t.Context(), created.Artifact.ID, request, a)
	if err != nil {
		t.Fatal(err)
	}
	download, err := api.DownloadArtifact(t.Context(), exported.ID, a)
	if err != nil || string(download.Data) != input.Content.Markdown || download.Export.Downloads != 1 {
		t.Fatal("download used latest instead of exported version", err)
	}
	for _, key := range []string{"artifact_read", "artifact_export"} {
		policy.denied.Store(key)
		_, err = api.DownloadArtifact(t.Context(), exported.ID, a)
		artifactErrorClass(t, err, "forbidden")
		metadata, err := repo.ArtifactExport(t.Context(), exported.ID, a)
		if err != nil || metadata.Downloads != 1 {
			t.Fatal("denied download was recorded", err)
		}
	}
	policy.denied.Store("")
	for _, field := range []string{"user", "workspace", "runtime"} {
		other := a
		class := "not_found"
		switch field {
		case "user":
			other.UserID = "other"
		case "workspace":
			other.WorkspaceID = "other"
		case "runtime":
			other.RuntimeID, class = "other", "forbidden"
		}
		_, err = api.DownloadArtifact(t.Context(), exported.ID, other)
		artifactErrorClass(t, err, class)
	}
	// Changed server TTL must not turn a lost-response retry into a new export.
	service.Close()
	options.ArtifactExportTTL = 2 * time.Hour
	restarted, err := application.NewConversationService(repo, nil, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	reExported, err := restarted.ExportArtifact(t.Context(), created.Artifact.ID, request, a)
	if err != nil || !reExported.ExpiresAt.Equal(exported.ExpiresAt) || reExported.ID != exported.ID {
		t.Fatal("restarted export lost original expiry/receipt", err)
	}
}

func TestArtifactProvenanceProtectsMetadataEditsAndExportsAfterRestart(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	var visible atomic.Bool
	visible.Store(true)
	knowledge := sourceKnowledgeFixture(t, &visible)
	policy := &artifactTestPolicy{}
	personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &sourceModel{step: func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if in.Messages[len(in.Messages)-1].Role == "user" {
			return resultToolCall("knowledge_search", "source", map[string]string{"query": "费用规则"}), nil
		}
		return sourceAnswer("PRIVATE-VALUE-97"), nil
	}}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{Knowledge: knowledge, PersonalAuthorizer: policy, ToolHost: personal})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "artifact-origin"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "lookup", Message: "费用规则"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, conversation.ID, run.ID); done.Status != "completed" {
		t.Fatalf("source run failed: %s", done.ErrorCode)
	}
	input := agentsdk.ConversationArtifactCreate{ClientID: "derived", Title: "PRIVATE-VALUE-97 周报", SourceConversationID: conversation.ID, SourceRunID: run.ID, Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "PRIVATE-VALUE-97"}}
	created, err := service.CreateArtifact(t.Context(), input, a)
	if err != nil {
		t.Fatal(err)
	}
	record, err := repo.ArtifactRecord(t.Context(), created.Artifact.ID, 1, a)
	if err != nil || len(record.Sources.Runs) != 1 || record.Sources.Runs[0].RunID != run.ID {
		t.Fatal("trusted provenance missing", err)
	}
	exported, err := service.ExportArtifact(t.Context(), created.Artifact.ID, agentsdk.ConversationArtifactExportRequest{ClientID: "private-export", Version: 1, Format: "markdown"}, a)
	if err != nil {
		t.Fatal(err)
	}
	other := a
	other.UserID = "other"
	_, err = service.CreateArtifact(t.Context(), input, other)
	artifactErrorClass(t, err, "not_found")
	input.SourceRunID = ""
	_, err = service.CreateArtifact(t.Context(), input, a)
	artifactErrorClass(t, err, "bad_request")
	service.Close()
	service, err = application.NewConversationService(repo, nil, a.RuntimeID, application.ConversationOptions{Knowledge: knowledge, PersonalAuthorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	visible.Store(false)
	if _, err = service.Artifact(t.Context(), created.Artifact.ID, 1, a); err == nil {
		t.Fatal("revoked document was exposed through an artifact")
	}
	page, err := service.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{}, a)
	if err != nil || len(page.Items) != 0 || !page.Omitted || strings.Contains(string(mustJSON(page)), "PRIVATE-VALUE-97") {
		t.Fatal("revoked artifact metadata escaped", err)
	}
	versions, err := service.ArtifactVersions(t.Context(), created.Artifact.ID, 0, 20, a)
	if err != nil || len(versions.Items) != 0 || !versions.Omitted {
		t.Fatal("revoked revision metadata escaped", err)
	}
	title := "new title"
	if _, err = service.EditArtifact(t.Context(), created.Artifact.ID, agentsdk.ConversationArtifactEdit{ClientID: "revoked-edit", ExpectedVersion: 1, Patch: agentsdk.ConversationArtifactPatch{Title: &title}}, a); err == nil {
		t.Fatal("revoked source still editable")
	}
	if _, err = service.DownloadArtifact(t.Context(), exported.ID, a); err == nil {
		t.Fatal("old export bypassed revoked source")
	}
	metadata, err := repo.ArtifactExport(t.Context(), exported.ID, a)
	if err != nil || metadata.Downloads != 0 {
		t.Fatal("source-denied download was counted", err)
	}
	visible.Store(true)
	download, err := service.DownloadArtifact(t.Context(), exported.ID, a)
	if err != nil || string(download.Data) != created.Content.Markdown {
		t.Fatal("restored source did not restore exact export", err)
	}
}

func TestArtifactsRejectCorruptHostContentBeforePersisting(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	storage, policy := &corruptArtifactStorage{}, &artifactTestPolicy{}
	service, err := application.NewConversationService(repo, nil, a.RuntimeID, application.ConversationOptions{PersonalAuthorizer: policy, ArtifactStorage: storage})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	input := agentsdk.ConversationArtifactCreate{ClientID: "corrupt-host", Title: "周报", Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: strings.Repeat("x", 40000)}}
	policy.denied.Store("artifact_create")
	_, err = service.CreateArtifact(t.Context(), input, a)
	artifactErrorClass(t, err, "forbidden")
	if storage.writes != 0 {
		t.Fatal("denied create touched content storage")
	}
	policy.denied.Store("")
	_, err = service.CreateArtifact(t.Context(), input, a)
	artifactErrorClass(t, err, "unavailable")
	page, err := repo.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{}, a)
	if err != nil || len(page.Items) != 0 || storage.writes != 1 {
		t.Fatal("corrupt storage reference was persisted", err)
	}
}
