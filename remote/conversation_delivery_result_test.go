package remote

import (
	"context"
	"net/http/httptest"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type releasedResultService struct {
	sdk.ConversationService
	sdk.ConversationCollaborationService
	input     sdk.ConversationDeliveryResultRead
	authority sdk.ConversationAuthority
	id        string
	calls     int
	denied    bool
	artifact  sdk.ConversationDeliveryArtifactRead
}

func (s *releasedResultService) ReadConversationDeliveryArtifact(_ context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactVersion, error) {
	s.calls++
	s.id, s.artifact, s.authority = id, in, a
	if s.denied {
		return sdk.ConversationArtifactVersion{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.delivery_artifact_not_released"}
	}
	return sdk.ConversationArtifactVersion{Artifact: sdk.ConversationArtifact{ID: in.ArtifactID, Version: in.Version}, Content: sdk.ConversationArtifactContent{Kind: "markdown", Markdown: "immutable body"}}, nil
}

func (s *releasedResultService) DownloadConversationDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactDownload, error) {
	_, err := s.ReadConversationDeliveryArtifact(ctx, id, in, a)
	if err != nil {
		return sdk.ConversationArtifactDownload{}, err
	}
	return sdk.ConversationArtifactDownload{Export: sdk.ConversationArtifactExport{ID: in.ExportID, ArtifactID: in.ArtifactID, Version: in.Version}, Data: []byte("immutable body")}, nil
}

func (s *releasedResultService) ReadConversationDeliveryResult(_ context.Context, id string, in sdk.ConversationDeliveryResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	s.calls++
	s.id, s.input, s.authority = id, in, a
	if s.denied {
		return sdk.ConversationResultSlice{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.delivery_result_not_released"}
	}
	return sdk.ConversationResultSlice{Reference: in.Reference, JSONText: "receipt", Offset: in.Offset, NextOffset: in.Offset + 7, TotalBytes: in.Offset + 7, Complete: true}, nil
}

func TestSaaSDeliveryReaderPreservesRevisionReferenceAndCurrentDenial(t *testing.T) {
	source := &releasedResultService{}
	svc, err := server.New(server.Config{APIKey: "receipt-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "receipt-runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	reader, ok := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationDeliveryResultReader)
	if !ok {
		t.Fatal("SaaS binding omitted released result reader")
	}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	in := sdk.ConversationDeliveryResultRead{DeliveryRevision: 17, ConversationResultRead: sdk.ConversationResultRead{Reference: sdk.ConversationResultReference{ConversationID: "producer", RunID: "run", Step: 4, CallID: "original", SHA256: "exact-source-hash"}, Offset: 256, MaxBytes: 512}}
	out, err := reader.ReadConversationDeliveryResult(t.Context(), "released", in, a)
	expected := a
	expected.RuntimeID = "runtime"
	if err != nil || source.id != "released" || source.input != in || source.authority != expected || out.Reference != in.Reference || out.JSONText != "receipt" {
		t.Fatalf("source-owned request lost in signed SaaS dispatch: %+v %+v %v", source, out, err)
	}
	source.denied = true
	if out, err := reader.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || out.JSONText != "" || source.calls != 2 {
		t.Fatal("current owner denial was hidden", err)
	}
	a.WorkspaceID = "other"
	if _, err := reader.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || source.calls != 2 {
		t.Fatal("cross-workspace request reached source", err)
	}
	source.denied = false
	a.WorkspaceID = "workspace"
	resources := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationDeliveryArtifactReader)
	resource := sdk.ConversationDeliveryArtifactRead{DeliveryRevision: 17, Reference: in.Reference, ArtifactID: "art_saved", Version: 2}
	value, err := resources.ReadConversationDeliveryArtifact(t.Context(), "released", resource, a)
	if err != nil || value.Artifact.ID != resource.ArtifactID || value.Artifact.Version != 2 || source.artifact != resource || source.authority != a {
		t.Fatal("full artifact reference changed in SaaS", err)
	}
	resource.ExportID = "export_saved"
	file, err := resources.DownloadConversationDeliveryArtifact(t.Context(), "released", resource, a)
	if err != nil || file.Export.ID != resource.ExportID || string(file.Data) != "immutable body" || source.artifact != resource || source.calls != 4 {
		t.Fatal("original export changed in SaaS", err)
	}
	source.denied = true
	if value, err := resources.ReadConversationDeliveryArtifact(t.Context(), "released", resource, a); err == nil || value.Content.Markdown != "" {
		t.Fatal("artifact source denial ignored")
	}
	if file, err := resources.DownloadConversationDeliveryArtifact(t.Context(), "released", resource, a); err == nil || len(file.Data) != 0 {
		t.Fatal("export source denial ignored")
	}
}
