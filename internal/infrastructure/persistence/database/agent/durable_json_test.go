package agent

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

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
