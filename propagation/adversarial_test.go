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

func TestInterruptedPropagationRollsBackCompletedTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	type interruptExec struct{ recoveryExec }
	_ = interruptExec{}
	calls := 0
	ex := executorFuncs{
		validate: func(context.Context, string, Plan) error { return nil },
		apply: func(_ context.Context, node string, _ Plan) ([]AppliedRevision, error) {
			calls++
			if node == "b" {
				cancel()
				return nil, ctx.Err()
			}
			return []AppliedRevision{{Kind: "monitor", ID: node, Before: 1, After: 2, Reversible: true}}, nil
		},
		rollback: func(context.Context, string, Plan, []AppliedRevision) error { return nil },
	}
	r, err := Execute(ctx, Plan{ID: "interrupted"}, []string{"a", "b"}, ex, true, func() time.Time { return time.Unix(2, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !r.Results[0].RolledBack || r.Results[1].Error == "" {
		t.Fatalf("interruption not visible/recovered: %+v", r)
	}
}

func TestAutomaticReconciliationRecoversAfterDestinationRestart(t *testing.T) {
	e := Envelope{Kind: "monitor", ID: "m", SchemaVersion: 1, Revision: 1, SourceNode: "source", Target: "members", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"enabled":true}`)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	p := Profile{ID: "restart", Name: "restart", Kinds: []string{"monitor"}, Selector: Selector{All: true}, Mode: ReconcileAutomatic, Enabled: true}
	members := []Member{{ID: "dest", Enabled: true}}
	offline := true
	applied := 0
	x := ProfileExecutor{
		Members: members,
		Export:  func(context.Context, []string, Actor, string) ([]Envelope, error) { return []Envelope{e}, nil },
		Preview: func(context.Context, string, []Envelope, Actor) (Preview, error) {
			if offline {
				return Preview{}, errors.New("destination restarting")
			}
			return Preview{Applicable: true, Items: []PlanItem{{Kind: "monitor", ID: "m", Change: ChangeCreate}}}, nil
		},
		Apply: func(context.Context, string, string, []Envelope, Actor) (ApplyBundleResult, error) {
			applied++
			return ApplyBundleResult{Revisions: []AppliedRevision{{Kind: "monitor", ID: "m", After: 1, Reversible: true}}}, nil
		},
		Now: func() time.Time { return time.Unix(3, 0) },
	}
	if _, err := ExecuteProfile(context.Background(), p, Actor{Permission: "monitor.update"}, x); err == nil || applied != 0 {
		t.Fatalf("offline destination should block mutation, applied=%d err=%v", applied, err)
	}
	offline = false
	r, err := ExecuteProfile(context.Background(), p, Actor{Permission: "monitor.update"}, x)
	if err != nil || applied != 1 || r.Applied != 1 {
		t.Fatalf("restart recovery failed: run=%+v applied=%d err=%v", r, applied, err)
	}
}

func TestRemovedSourceAndRevokedDestinationFailClosed(t *testing.T) {
	p := Profile{ID: "source", Name: "source", Kinds: []string{"monitor"}, Selector: Selector{All: true}, Mode: ReconcileAutomatic, Enabled: true}
	x := ProfileExecutor{
		Members: []Member{{ID: "dest", Enabled: true}},
		Export: func(context.Context, []string, Actor, string) ([]Envelope, error) {
			return nil, errors.New("source removed")
		},
		Preview: func(context.Context, string, []Envelope, Actor) (Preview, error) {
			t.Fatal("preview called after source removal")
			return Preview{}, nil
		},
	}
	if _, err := ExecuteProfile(context.Background(), p, Actor{}, x); err == nil {
		t.Fatal("removed source accepted")
	}

	ex := executorFuncs{
		validate: func(context.Context, string, Plan) error { return nil },
		apply: func(context.Context, string, Plan) ([]AppliedRevision, error) {
			return nil, errors.New("credential revoked mid-flight")
		},
		rollback: func(context.Context, string, Plan, []AppliedRevision) error { return nil },
	}
	r, _ := Execute(context.Background(), Plan{ID: "revoked-mid-flight"}, []string{"dest"}, ex, true, time.Now)
	if len(r.Results) != 1 || r.Results[0].Error == "" || r.Results[0].Applied {
		t.Fatalf("revocation hidden: %+v", r)
	}
}

type executorFuncs struct {
	validate func(context.Context, string, Plan) error
	apply    func(context.Context, string, Plan) ([]AppliedRevision, error)
	rollback func(context.Context, string, Plan, []AppliedRevision) error
}

func (e executorFuncs) Validate(ctx context.Context, node string, p Plan) error {
	return e.validate(ctx, node, p)
}
func (e executorFuncs) Apply(ctx context.Context, node string, p Plan) ([]AppliedRevision, error) {
	return e.apply(ctx, node, p)
}
func (e executorFuncs) Rollback(ctx context.Context, node string, p Plan, r []AppliedRevision) error {
	return e.rollback(ctx, node, p, r)
}
