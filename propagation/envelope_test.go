package propagation

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeDigestAndSecretReferences(t *testing.T) {
	e := Envelope{Kind: "monitor", ID: "m1", SchemaVersion: 1, Revision: 2, SourceNode: "n1", Target: "all", Conflict: ConflictReject, Payload: json.RawMessage(`{"b":2,"a":1}`), Secrets: []SecretRef{{Name: "pagerduty", Required: true}}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if e.Digest == "" {
		t.Fatal("missing digest")
	}
	e.Payload = json.RawMessage(`{"a":1,"b":2}`)
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
