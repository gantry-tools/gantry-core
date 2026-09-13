package propagation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type AppliedRevision struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Before     int64  `json:"before_revision"`
	After      int64  `json:"after_revision"`
	Reversible bool   `json:"reversible"`
}
type NodeResult struct {
	Node       string            `json:"node"`
	Validated  bool              `json:"validated"`
	Applied    bool              `json:"applied"`
	RolledBack bool              `json:"rolled_back"`
	Revisions  []AppliedRevision `json:"revisions,omitempty"`
	Error      string            `json:"error,omitempty"`
}
type Executor interface {
	Validate(context.Context, string, Plan) error
	Apply(context.Context, string, Plan) ([]AppliedRevision, error)
	Rollback(context.Context, string, Plan, []AppliedRevision) error
}
type TransactionResult struct {
	PlanID     string       `json:"plan_id"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	Results    []NodeResult `json:"results"`
	Partial    bool         `json:"partial"`
}

func Execute(ctx context.Context, plan Plan, nodes []string, ex Executor, rollbackOnFailure bool, now func() time.Time) (TransactionResult, error) {
	if ex == nil {
		return TransactionResult{}, errors.New("executor required")
	}
	if now == nil {
		now = time.Now
	}
	out := TransactionResult{PlanID: plan.ID, StartedAt: now().UTC()}
	for _, n := range nodes {
		r := NodeResult{Node: n}
		if err := ex.Validate(ctx, n, plan); err != nil {
			r.Error = err.Error()
			out.Results = append(out.Results, r)
			continue
		}
		r.Validated = true
		out.Results = append(out.Results, r)
	}
	for i := range out.Results {
		r := &out.Results[i]
		if !r.Validated {
			continue
		}
		revs, err := ex.Apply(ctx, r.Node, plan)
		if err != nil {
			r.Error = err.Error()
			if rollbackOnFailure {
				for j := 0; j < i; j++ {
					prev := &out.Results[j]
					if !prev.Applied {
						continue
					}
					if rb := ex.Rollback(ctx, prev.Node, plan, prev.Revisions); rb == nil {
						prev.RolledBack = true
					} else if prev.Error == "" {
						prev.Error = fmt.Sprintf("rollback: %v", rb)
					}
				}
			}
			continue
		}
		r.Applied = true
		r.Revisions = revs
	}
	successes := 0
	for _, r := range out.Results {
		if r.Applied && !r.RolledBack {
			successes++
		}
	}
	out.Partial = successes > 0 && successes < len(out.Results)
	out.FinishedAt = now().UTC()
	return out, nil
}
