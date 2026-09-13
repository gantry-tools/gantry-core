package propagation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type certifiedNode struct {
	mu       sync.Mutex
	existing map[string]Existing
}

func newCertifiedNode() *certifiedNode { return &certifiedNode{existing: map[string]Existing{}} }

func (n *certifiedNode) preview(_ context.Context, _ string, src []Envelope, actor Actor) (Preview, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return DryRun(src, TargetState{
		Existing:         cloneExisting(n.existing),
		SupportedSchemas: map[string]int{"monitor": 1},
		Dependencies:     map[string]bool{},
		Secrets:          MapSecrets{},
		Permissions:      map[string]bool{actor.Permission: true},
	}, false)
}

func (n *certifiedNode) apply(_ context.Context, _ string, _ string, src []Envelope, _ Actor) (ApplyBundleResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := ApplyBundleResult{}
	for _, e := range src {
		n.existing[e.Kind+"/"+e.ID] = Existing{Kind: e.Kind, ID: e.ID, Digest: e.Digest, Revision: e.Revision, SchemaVersion: e.SchemaVersion, Owner: e.SourceNode}
		out.Revisions = append(out.Revisions, AppliedRevision{Kind: e.Kind, ID: e.ID, After: e.Revision, Reversible: true})
	}
	return out, nil
}

func cloneExisting(in map[string]Existing) map[string]Existing {
	out := make(map[string]Existing, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestPhase6ThreeNodePropagationCertification(t *testing.T) {
	e := Envelope{
		Kind: "monitor", ID: "uptime", SchemaVersion: 1, Revision: 1,
		SourceNode: "source", Target: "members", Conflict: ConflictSourceWins,
		Actor:   Actor{Kind: "human", ID: "admin", Permission: "monitor.update"},
		Payload: json.RawMessage(`{"interval":"1m","enabled":true}`),
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	nodes := map[string]*certifiedNode{"n1": newCertifiedNode(), "n2": newCertifiedNode(), "n3": newCertifiedNode()}
	members := []Member{{ID: "n1", Enabled: true}, {ID: "n2", Enabled: true}, {ID: "n3", Enabled: true}}
	profile := Profile{ID: "cert", Name: "cert", Kinds: []string{"monitor"}, Selector: Selector{All: true}, Mode: ReconcileAutomatic, Enabled: true}
	actor := e.Actor
	x := ProfileExecutor{
		Members: members,
		Export:  func(context.Context, []string, Actor, string) ([]Envelope, error) { return []Envelope{e}, nil },
		Preview: func(ctx context.Context, node string, src []Envelope, a Actor) (Preview, error) {
			return nodes[node].preview(ctx, node, src, a)
		},
		Apply: func(ctx context.Context, node, plan string, src []Envelope, a Actor) (ApplyBundleResult, error) {
			return nodes[node].apply(ctx, node, plan, src, a)
		},
		Now: func() time.Time { return time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) },
	}

	first, err := ExecuteProfile(context.Background(), profile, actor, x)
	if err != nil {
		t.Fatal(err)
	}
	if first.Drift != 3 || first.Applied != 3 || first.Failed != 0 || first.Action != "apply" {
		t.Fatalf("fresh three-node apply not certified: %+v", first)
	}
	second, err := ExecuteProfile(context.Background(), profile, actor, x)
	if err != nil {
		t.Fatal(err)
	}
	if second.Drift != 0 || second.Applied != 0 || second.Failed != 0 || second.Action != "none" {
		t.Fatalf("post-apply drift not clean: %+v", second)
	}

	// A partition must remain visible and block automatic reconciliation.
	e.Revision = 2
	e.Payload = json.RawMessage(`{"interval":"30s","enabled":true}`)
	e.Digest = ""
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	x.Export = func(context.Context, []string, Actor, string) ([]Envelope, error) { return []Envelope{e}, nil }
	basePreview := x.Preview
	x.Preview = func(ctx context.Context, node string, src []Envelope, a Actor) (Preview, error) {
		if node == "n2" {
			return Preview{}, errors.New("partition")
		}
		return basePreview(ctx, node, src, a)
	}
	blocked, err := ExecuteProfile(context.Background(), profile, actor, x)
	if err == nil || blocked.Failed != 1 || blocked.Applied != 0 {
		t.Fatalf("partial failure concealed: run=%+v err=%v", blocked, err)
	}
}
