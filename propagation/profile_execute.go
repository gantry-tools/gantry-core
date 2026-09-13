package propagation

import (
	"context"
	"fmt"
	"time"
)

// ProfileExecutor supplies the product-specific cluster operations needed to
// evaluate and, when allowed, reconcile one saved propagation profile.
type ProfileExecutor struct {
	Members []Member
	Export  func(context.Context, []string, Actor, string) ([]Envelope, error)
	Preview RemotePreviewFunc
	Apply   RemoteApplyFunc
	Now     func() time.Time
}

func (x ProfileExecutor) now() time.Time {
	if x.Now != nil {
		return x.Now().UTC()
	}
	return time.Now().UTC()
}

// ExecuteProfile evaluates one profile against its selected cluster members.
// Notify-only and approval-required modes never mutate destinations. Automatic
// mode applies only when every selected target produced an applicable preview.
func ExecuteProfile(ctx context.Context, p Profile, actor Actor, x ProfileExecutor) (ProfileRunResult, error) {
	out := ProfileRunResult{ProfileID: p.ID, Action: "none"}
	if err := p.Validate(); err != nil {
		return out, err
	}
	if x.Export == nil || x.Preview == nil {
		return out, fmt.Errorf("profile export and preview callbacks required")
	}
	nodes, err := Select(p.Selector, x.Members)
	if err != nil {
		return out, err
	}
	if len(nodes) == 0 {
		return out, nil
	}
	env, err := x.Export(ctx, p.Kinds, actor, "members")
	if err != nil {
		return out, err
	}
	previews := PreviewNodes(ctx, nodes, env, actor, 4, x.Preview)
	blocked := false
	for _, n := range previews {
		if n.Error != "" {
			out.Failed++
			blocked = true
			continue
		}
		if !n.Preview.Applicable {
			out.Failed++
			blocked = true
		}
		for _, item := range n.Preview.Items {
			if item.Change != ChangeNoop {
				out.Drift++
			}
		}
	}
	if out.Drift == 0 && out.Failed == 0 {
		return out, nil
	}
	decision := DecideReconcile(p, make([]Drift, out.Drift), x.now())
	out.Action = decision.Action
	switch decision.Action {
	case "none", "notify", "plan":
		return out, nil
	case "apply":
		if blocked {
			return out, fmt.Errorf("automatic reconciliation blocked by failed or inapplicable preview")
		}
		if x.Apply == nil {
			return out, fmt.Errorf("profile apply callback required for automatic reconciliation")
		}
		planID := fmt.Sprintf("profile-%s-%d", p.ID, x.now().UnixNano())
		applied := ApplyNodes(ctx, nodes, planID, env, actor, 4, x.Apply)
		out.Failed = 0
		for _, n := range applied {
			if n.Error != "" {
				out.Failed++
				continue
			}
			out.Applied += len(n.Result.Revisions)
		}
		if out.Failed > 0 {
			return out, fmt.Errorf("automatic reconciliation partially failed on %d target(s)", out.Failed)
		}
		return out, nil
	default:
		return out, fmt.Errorf("unsupported reconcile action %q", decision.Action)
	}
}
