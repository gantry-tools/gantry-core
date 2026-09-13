package propagation

import (
	"context"
	"testing"
	"time"
)

func TestExecuteProfileModesAndAutomaticApply(t *testing.T) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	members := []Member{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}
	export := func(context.Context, []string, Actor, string) ([]Envelope, error) {
		return []Envelope{{Kind: "monitor", ID: "m1", SchemaVersion: 1, Revision: 2, Digest: "new"}}, nil
	}
	preview := func(context.Context, string, []Envelope, Actor) (Preview, error) {
		return Preview{Applicable: true, Items: []PlanItem{{Kind: "monitor", ID: "m1", Change: ChangeUpdate}}}, nil
	}
	applies := 0
	apply := func(context.Context, string, string, []Envelope, Actor) (ApplyBundleResult, error) {
		applies++
		return ApplyBundleResult{Revisions: []AppliedRevision{{Kind: "monitor", ID: "m1"}}}, nil
	}
	x := ProfileExecutor{Members: members, Export: export, Preview: preview, Apply: apply, Now: func() time.Time { return now }}
	for _, tc := range []struct {
		mode   ReconcileMode
		action string
		apply  int
	}{{ReconcileNotify, "notify", 0}, {ReconcileApproval, "plan", 0}, {ReconcileAutomatic, "apply", 2}} {
		applies = 0
		p := Profile{ID: "p", Name: "p", Selector: Selector{All: true}, Kinds: []string{"monitor"}, Mode: tc.mode, Enabled: true}
		r, err := ExecuteProfile(context.Background(), p, Actor{Kind: "system", ID: "scheduler"}, x)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != tc.action || applies != tc.apply || r.Drift != 2 {
			t.Fatalf("mode %s: result=%+v applies=%d", tc.mode, r, applies)
		}
	}
}

func TestExecuteProfileBlocksAutomaticOnPreviewFailure(t *testing.T) {
	x := ProfileExecutor{
		Members: []Member{{ID: "a", Enabled: true}},
		Export: func(context.Context, []string, Actor, string) ([]Envelope, error) {
			return []Envelope{{Kind: "monitor", ID: "m1", SchemaVersion: 1, Revision: 1, Digest: "x"}}, nil
		},
		Preview: func(context.Context, string, []Envelope, Actor) (Preview, error) {
			return Preview{Applicable: false, Items: []PlanItem{{Kind: "monitor", ID: "m1", Change: ChangeUpdate}}}, nil
		},
		Apply: func(context.Context, string, string, []Envelope, Actor) (ApplyBundleResult, error) {
			t.Fatal("apply must not run when preview is blocked")
			return ApplyBundleResult{}, nil
		},
	}
	p := Profile{ID: "p", Name: "p", Selector: Selector{All: true}, Kinds: []string{"monitor"}, Mode: ReconcileAutomatic, Enabled: true}
	r, err := ExecuteProfile(context.Background(), p, Actor{Kind: "system"}, x)
	if err == nil || r.Failed != 1 {
		t.Fatalf("expected blocked automatic reconciliation, result=%+v err=%v", r, err)
	}
}
