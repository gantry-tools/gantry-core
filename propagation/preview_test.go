package propagation

import (
	"encoding/json"
	"testing"
)

func TestDryRunReportsCompatibilityAndRequirements(t *testing.T) {
	e := Envelope{Kind: "monitor", ID: "m", SchemaVersion: 2, Revision: 1, SourceNode: "n", Target: "all", Conflict: ConflictReject, Actor: Actor{Permission: "monitor.update"}, Dependencies: []string{"dep"}, Secrets: []SecretRef{{Name: "secret", Required: true}}, Payload: json.RawMessage(`{"x":1}`)}
	p, err := DryRun([]Envelope{e}, TargetState{Existing: map[string]Existing{}, SupportedSchemas: map[string]int{"monitor": 1}, Dependencies: map[string]bool{}, Secrets: MapSecrets{}, Permissions: map[string]bool{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Applicable || len(p.Issues) != 4 {
		t.Fatalf("%+v", p)
	}
}
