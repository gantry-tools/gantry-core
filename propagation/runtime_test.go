package propagation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type memAdapter struct {
	objs map[string]ObjectState
	fail string
}

func (m *memAdapter) Kinds() []KindDescriptor {
	return []KindDescriptor{{Kind: "monitor", SchemaVersion: 1, Reversible: true}}
}
func (m *memAdapter) Export(_ context.Context, _ []string, a Actor, target string) ([]Envelope, error) {
	return nil, nil
}
func (m *memAdapter) Snapshot(_ context.Context, _ []string) (map[string]ObjectState, error) {
	out := map[string]ObjectState{}
	for k, v := range m.objs {
		out[k] = v
	}
	return out, nil
}
func (m *memAdapter) TargetState(_ context.Context, src []Envelope, a Actor) (TargetState, error) {
	e := map[string]Existing{}
	for k, v := range m.objs {
		e[k] = v.Existing
	}
	return TargetState{Existing: e, SupportedSchemas: map[string]int{"monitor": 1}, Permissions: map[string]bool{a.Permission: true}, Dependencies: map[string]bool{}, Secrets: MapSecrets{}}, nil
}
func (m *memAdapter) Apply(_ context.Context, e Envelope) (AppliedRevision, error) {
	if e.ID == m.fail {
		return AppliedRevision{}, errors.New("boom")
	}
	k := e.Kind + "/" + e.ID
	m.objs[k] = ObjectState{Existing: Existing{Kind: e.Kind, ID: e.ID, Digest: e.Digest, Revision: e.Revision}, Payload: e.Payload, Reversible: true}
	return AppliedRevision{Kind: e.Kind, ID: e.ID, After: e.Revision, Reversible: true}, nil
}
func (m *memAdapter) Restore(_ context.Context, s ObjectState) error {
	m.objs[s.Existing.Kind+"/"+s.Existing.ID] = s
	return nil
}
func TestApplyLocalRollsBackEarlierObject(t *testing.T) {
	a := &memAdapter{objs: map[string]ObjectState{"monitor/a": {Existing: Existing{Kind: "monitor", ID: "a", Digest: "old", Revision: 1}, Payload: json.RawMessage(`{"x":1}`), Reversible: true}}, fail: "b"}
	src := []Envelope{{Kind: "monitor", ID: "a", SchemaVersion: 1, Revision: 2, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"x":2}`)}, {Kind: "monitor", ID: "b", SchemaVersion: 1, Revision: 1, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"x":3}`)}}
	for i := range src {
		if err := src[i].Validate(); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ApplyLocal(context.Background(), a, src, Actor{Permission: "monitor.update"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if string(a.objs["monitor/a"].Payload) != `{"x":1}` {
		t.Fatalf("not restored: %s", a.objs["monitor/a"].Payload)
	}
}
