package propagation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type KindDescriptor struct {
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	SchemaVersion int    `json:"schema_version"`
	Reversible    bool   `json:"reversible"`
	Mergeable     bool   `json:"mergeable"`
}
type ObjectState struct {
	Existing   Existing        `json:"existing"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Reversible bool            `json:"reversible"`
}
type LocalAdapter interface {
	Kinds() []KindDescriptor
	Export(context.Context, []string, Actor, string) ([]Envelope, error)
	Snapshot(context.Context, []string) (map[string]ObjectState, error)
	TargetState(context.Context, []Envelope, Actor) (TargetState, error)
	Apply(context.Context, Envelope) (AppliedRevision, error)
	Restore(context.Context, ObjectState) error
}
type ApplyBundleResult struct {
	Revisions  []AppliedRevision `json:"revisions"`
	RolledBack bool              `json:"rolled_back"`
	Error      string            `json:"error,omitempty"`
}

func SupportedKindMap(a LocalAdapter) map[string]KindDescriptor {
	out := map[string]KindDescriptor{}
	for _, k := range a.Kinds() {
		out[k.Kind] = k
	}
	return out
}
func PreviewLocal(ctx context.Context, a LocalAdapter, source []Envelope, actor Actor, allowDelete bool) (Preview, error) {
	if a == nil {
		return Preview{}, errors.New("adapter required")
	}
	kinds := make([]string, 0, len(source))
	seen := map[string]bool{}
	for _, e := range source {
		if !seen[e.Kind] {
			seen[e.Kind] = true
			kinds = append(kinds, e.Kind)
		}
	}
	state, err := a.TargetState(ctx, source, actor)
	if err != nil {
		return Preview{}, err
	}
	return DryRun(source, state, allowDelete)
}
func ApplyLocal(ctx context.Context, a LocalAdapter, source []Envelope, actor Actor) (ApplyBundleResult, error) {
	if a == nil {
		return ApplyBundleResult{}, errors.New("adapter required")
	}
	p, err := PreviewLocal(ctx, a, source, actor, false)
	if err != nil {
		return ApplyBundleResult{}, err
	}
	if !p.Applicable {
		return ApplyBundleResult{}, fmt.Errorf("preview contains %d blocking issue(s)", len(p.Issues))
	}
	kinds := make([]string, 0, len(source))
	for _, e := range source {
		kinds = append(kinds, e.Kind)
	}
	before, err := a.Snapshot(ctx, kinds)
	if err != nil {
		return ApplyBundleResult{}, err
	}
	out := ApplyBundleResult{}
	applied := []string{}
	for _, e := range source {
		r, err := a.Apply(ctx, e)
		if err != nil {
			out.Error = err.Error()
			for i := len(applied) - 1; i >= 0; i-- {
				state, ok := before[applied[i]]
				if !ok {
					parts := strings.SplitN(applied[i], "/", 2)
					state = ObjectState{Existing: Existing{Kind: parts[0], ID: parts[1]}, Reversible: true}
				}
				if !state.Reversible {
					continue
				}
				if rb := a.Restore(ctx, state); rb != nil {
					out.Error += "; rollback " + applied[i] + ": " + rb.Error()
					continue
				}
				out.RolledBack = true
			}
			return out, err
		}
		out.Revisions = append(out.Revisions, r)
		applied = append(applied, e.Kind+"/"+e.ID)
	}
	sort.Slice(out.Revisions, func(i, j int) bool {
		if out.Revisions[i].Kind == out.Revisions[j].Kind {
			return out.Revisions[i].ID < out.Revisions[j].ID
		}
		return out.Revisions[i].Kind < out.Revisions[j].Kind
	})
	return out, nil
}
