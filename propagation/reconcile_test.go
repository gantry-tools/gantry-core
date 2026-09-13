package propagation

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDriftAndReconcileModes(t *testing.T) {
	e := Envelope{Kind: "monitor", ID: "m", SchemaVersion: 1, Revision: 1, SourceNode: "n", Target: "all", Conflict: ConflictReject, Payload: json.RawMessage(`{"x":1}`)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	d := DetectDrift("b", []Envelope{e}, map[string]Existing{})
	if len(d) != 1 {
		t.Fatal(d)
	}
	p := Profile{ID: "p", Name: "prod", Kinds: []string{"monitor"}, Mode: ReconcileApproval, Enabled: true}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r := DecideReconcile(p, d, time.Unix(1, 0))
	if r.Action != "plan" || !r.NeedsApproval {
		t.Fatalf("%+v", r)
	}
}
