package propagation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type recoveryExec struct {
	applyErr    map[string]error
	rollbackErr map[string]error
}

func (r recoveryExec) Validate(_ context.Context, node string, _ Plan) error {
	if node == "revoked" {
		return errors.New("credential revoked")
	}
	return nil
}
func (r recoveryExec) Apply(_ context.Context, node string, _ Plan) ([]AppliedRevision, error) {
	if err := r.applyErr[node]; err != nil {
		return nil, err
	}
	return []AppliedRevision{{Kind: "monitor", ID: "m", Before: 1, After: 2, Reversible: true}}, nil
}
func (r recoveryExec) Rollback(_ context.Context, node string, _ Plan, _ []AppliedRevision) error {
	return r.rollbackErr[node]
}

func TestAdversarialRecoveryMatrix(t *testing.T) {
	e := Envelope{Kind: "monitor", ID: "m", SchemaVersion: 2, Revision: 2, SourceNode: "source", Target: "all", Conflict: ConflictReject, Actor: Actor{Permission: "monitor.update"}, Secrets: []SecretRef{{Name: "pager", Required: true}}, Payload: json.RawMessage(`{"x":2}`)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	// stale/concurrent destination revision must not silently win.
	p, err := DryRun([]Envelope{e}, TargetState{Existing: map[string]Existing{"monitor/m": {Kind: "monitor", ID: "m", Revision: 2, Digest: "other"}}, SupportedSchemas: map[string]int{"monitor": 1}, Secrets: MapSecrets{}, Permissions: map[string]bool{"monitor.update": true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Applicable {
		t.Fatal("incompatible schema/missing secret accepted")
	}
	if p.Items[0].Change != ChangeConflict {
		t.Fatalf("stale revision did not conflict: %+v", p.Items[0])
	}
	// network partition/apply failure is surfaced and a prior successful node can be rolled back.
	r, err := Execute(context.Background(), Plan{ID: "p"}, []string{"a", "partition", "revoked"}, recoveryExec{applyErr: map[string]error{"partition": errors.New("partition")}, rollbackErr: map[string]error{}}, true, func() time.Time { return time.Unix(1, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if !r.Results[0].RolledBack || r.Results[1].Error == "" || r.Results[2].Error == "" {
		t.Fatalf("unexpected recovery result: %+v", r)
	}
	// rollback failure stays visible; it is never reported as atomic success.
	r, _ = Execute(context.Background(), Plan{ID: "p2"}, []string{"a", "partition"}, recoveryExec{applyErr: map[string]error{"partition": errors.New("partition")}, rollbackErr: map[string]error{"a": errors.New("rollback failed")}}, true, func() time.Time { return time.Unix(1, 0) })
	if r.Results[0].RolledBack || r.Results[0].Error == "" {
		t.Fatalf("rollback failure hidden: %+v", r)
	}
}

func TestDisabledTargetsAreExcluded(t *testing.T) {
	got, err := Select(Selector{All: true}, []Member{{ID: "live", Enabled: true}, {ID: "revoked", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("%v", got)
	}
}
