package application

import (
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestDeliveryArtifactResourceMustOccurInAnArtifactReceipt(t *testing.T) {
	meta := sdk.ConversationArtifact{ID: "art_original", Version: 3, Title: "original"}
	for _, key := range []string{"artifact_create", "artifact_edit", "artifact_read", "artifact_list", "artifact_versions", "artifact_export"} {
		t.Run(key, func(t *testing.T) {
			var value any = map[string]any{"artifact": meta}
			if key == "artifact_list" || key == "artifact_versions" {
				value = map[string]any{"items": []sdk.ConversationArtifact{meta}}
			} else if key == "artifact_export" {
				value = map[string]any{"export": sdk.ConversationArtifactExport{ID: "export_original", ArtifactID: meta.ID, Version: meta.Version}}
			}
			raw, _ := json.Marshal(value)
			record := persistence.ConversationToolExecution{Call: sdk.ConversationToolCall{Name: key}, Result: &sdk.ConversationToolResult{Status: "completed", Content: raw}}
			in := sdk.ConversationDeliveryArtifactRead{ArtifactID: meta.ID, Version: meta.Version}
			if !deliveryArtifactMatches(record, in) {
				t.Fatal("exact released artifact missing")
			}
			for _, changed := range []sdk.ConversationDeliveryArtifactRead{
				{ArtifactID: "art_other", Version: meta.Version},
				{ArtifactID: meta.ID, Version: meta.Version + 1},
				{ArtifactID: meta.ID, Version: 0},
				{ArtifactID: meta.ID, Version: meta.Version, ExportID: "export_other"},
			} {
				if deliveryArtifactMatches(record, changed) {
					t.Fatal("receipt released another resource", changed)
				}
			}
			in.ExportID = "export_original"
			if deliveryArtifactMatches(record, in) != (key == "artifact_export") {
				t.Fatal("artifact receipt implicitly released an export")
			}
			record.Call.Name = "business_query"
			in.ExportID = ""
			if deliveryArtifactMatches(record, in) {
				t.Fatal("business data with an artifact-shaped field released Knowledge")
			}
		})
	}
}
