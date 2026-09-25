package agent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

func TestDurableJSONUsesMillisAndPreservesRawEvidence(t *testing.T) {
	type payload struct {
		CreatedAt time.Time       `json:"created_at"`
		Evidence  json.RawMessage `json:"evidence"`
	}
	instant := time.Date(2026, 9, 25, 5, 0, 0, 123_000_000, time.UTC)
	evidence := json.RawMessage(`{"z":1,"a":{"second":2,"first":1}}`)
	raw, err := marshalDurableJSON(payload{CreatedAt: instant, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"created_at":1790312400123`)) {
		t.Fatalf("durable time is not Unix milliseconds: %s", raw)
	}
	var loaded payload
	if err := unmarshalDurableJSON(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !loaded.CreatedAt.Equal(instant) {
		t.Fatalf("time round trip=%s want=%s", loaded.CreatedAt, instant)
	}
	if !bytes.Equal(loaded.Evidence, evidence) {
		t.Fatalf("raw evidence changed: got=%s want=%s", loaded.Evidence, evidence)
	}
}

func TestDurableJSONRespectsCustomMillisecondUnmarshaler(t *testing.T) {
	instant := time.Date(2026, 9, 25, 5, 0, 0, 123_000_000, time.UTC)
	step := lifecyclemodel.SubjectExecutionStep{WorkspaceID: "workspace", RequestID: "request", Owner: "agent", Operation: "erase", Payload: json.RawMessage(`{"ok":true}`), CompletedAt: instant}
	raw, err := marshalDurableJSON(step)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"completed_at":1790312400123`)) {
		t.Fatalf("durable step time is not Unix milliseconds: %s", raw)
	}
	var loaded lifecyclemodel.SubjectExecutionStep
	if err := unmarshalDurableJSON(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !loaded.CompletedAt.Equal(instant) || !bytes.Equal(loaded.Payload, step.Payload) {
		t.Fatalf("durable step round trip=%+v", loaded)
	}
}

func TestDurableJSONKeepsOpaqueResultContentUnchanged(t *testing.T) {
	type result struct {
		Content json.RawMessage `json:"content"`
	}
	value := result{Content: json.RawMessage(`{"z":1,"artifact":{"updated_at":"2026-09-25T05:00:00.123Z","a":2},"first":3}`)}
	raw, err := marshalDurableJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, value.Content) {
		t.Fatalf("opaque content changed on write: %s", raw)
	}
	var loaded result
	if err := unmarshalDurableJSON(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Content, value.Content) {
		t.Fatalf("nested signed transport bytes changed: got=%s want=%s", loaded.Content, value.Content)
	}
}

func TestDurableJSONPreservesEmptyOptionalRawField(t *testing.T) {
	type result struct {
		Content json.RawMessage `json:"content"`
	}
	want := result{Content: json.RawMessage(`{"source":{"queried_at":"","proof":"owner"}}`)}
	raw, err := marshalDurableJSON(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, want.Content) {
		t.Fatalf("opaque optional field changed on write: %s", raw)
	}
	var got result
	if err := unmarshalDurableJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Content, want.Content) {
		t.Fatalf("optional transport time changed: got=%s want=%s", got.Content, want.Content)
	}
}

func TestDurableJSONPreservesEmbeddedDisagreement(t *testing.T) {
	instant := time.Date(2026, 9, 25, 5, 0, 0, 123_000_000, time.UTC)
	value := sdk.ConversationDisagreement{
		ConversationDisagreementSummary: sdk.ConversationDisagreementSummary{
			ID: "dispute", Revision: 1, Title: "Clock receipt", Status: "open", BriefVersion: 1,
			AgreementRevision: 1, DeliveryDigest: "digest", OwnerAgentID: "agent", NextAction: "review",
			Sources: []sdk.ConversationRunReference{{ConversationID: "conversation", RunID: "run", BeforeStep: 2}}, UpdatedAt: instant,
		},
		Claims: []sdk.ConversationDisagreementClaim{{
			ID: "claim", ConversationDisagreementClaimInput: sdk.ConversationDisagreementClaimInput{
				Conclusion: "Current execution", Receipts: []sdk.ConversationResultReference{{ConversationID: "conversation", RunID: "run", Step: 1, CallID: "clock", SHA256: "digest"}},
			},
			Actor:     sdk.ConversationDisagreementActor{UserID: "user", AgentID: "agent", Source: &sdk.ConversationRunReference{ConversationID: "conversation", RunID: "run", BeforeStep: 2}},
			CreatedAt: instant,
		}},
		Actor: sdk.ConversationDisagreementActor{UserID: "user"}, Event: "raise", Reason: "reason",
	}
	raw, err := marshalDurableJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	var got sdk.ConversationDisagreement
	if err := unmarshalDurableJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, value) {
		wantJSON, _ := json.Marshal(value)
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("durable embedded type changed: stored=%s\nwant=%s\ngot=%s", raw, wantJSON, gotJSON)
	}
}
