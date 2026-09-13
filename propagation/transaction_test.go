package propagation

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeExec struct{ fail string }

func (f fakeExec) Validate(_ context.Context, n string, _ Plan) error {
	if n == "bad-validate" {
		return errors.New("invalid")
	}
	return nil
}
func (f fakeExec) Apply(_ context.Context, n string, _ Plan) ([]AppliedRevision, error) {
	if n == f.fail {
		return nil, errors.New("apply failed")
	}
	return []AppliedRevision{{Kind: "monitor", ID: "m", Before: 1, After: 2, Reversible: true}}, nil
}
func (f fakeExec) Rollback(_ context.Context, _ string, _ Plan, _ []AppliedRevision) error {
	return nil
}
func TestTransactionShowsPartialAndRollback(t *testing.T) {
	now := func() time.Time { return time.Unix(1, 0) }
	r, err := Execute(context.Background(), Plan{ID: "p"}, []string{"a", "b"}, fakeExec{fail: "b"}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Partial || !r.Results[0].Applied || r.Results[1].Error == "" {
		t.Fatalf("%+v", r)
	}
	r, _ = Execute(context.Background(), Plan{ID: "p"}, []string{"a", "b"}, fakeExec{fail: "b"}, true, now)
	if !r.Results[0].RolledBack {
		t.Fatal("expected rollback")
	}
}
