package propagation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
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

func TestApplyLocalCountsOnlyActuallyRolledBackObjects(t *testing.T) {
	a := &memAdapter{objs: map[string]ObjectState{
		"monitor/a": {Existing: Existing{Kind: "monitor", ID: "a", Digest: "old", Revision: 1}, Payload: json.RawMessage(`{"x":1}`), Reversible: false},
		"monitor/b": {Existing: Existing{Kind: "monitor", ID: "b", Digest: "old", Revision: 1}, Payload: json.RawMessage(`{"y":1}`), Reversible: true},
	}, fail: "c"}
	src := []Envelope{
		{Kind: "monitor", ID: "a", SchemaVersion: 1, Revision: 2, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"x":2}`)},
		{Kind: "monitor", ID: "b", SchemaVersion: 1, Revision: 2, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"y":2}`)},
		{Kind: "monitor", ID: "c", SchemaVersion: 1, Revision: 1, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"z":1}`)},
	}
	for i := range src {
		if err := src[i].Validate(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ApplyLocal(context.Background(), a, src, Actor{Permission: "monitor.update"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(result.Revisions) != 2 {
		t.Fatalf("expected 2 applied revisions, got %d", len(result.Revisions))
	}
	if !result.RolledBack {
		t.Fatal("expected partial rollback to be reported")
	}
	if result.RolledBackCount != 1 {
		t.Fatalf("expected rolled_back_count=1, got %d", result.RolledBackCount)
	}
	if string(a.objs["monitor/a"].Payload) != `{"x":2}` {
		t.Fatalf("non-reversible object must stay applied, got %s", a.objs["monitor/a"].Payload)
	}
	if string(a.objs["monitor/b"].Payload) != `{"y":1}` {
		t.Fatalf("reversible object must be restored, got %s", a.objs["monitor/b"].Payload)
	}
}

func TestManagerHistoryCountsPartialRollbackCorrectly(t *testing.T) {
	st := &stateMem{}
	a := &memAdapter{objs: map[string]ObjectState{
		"monitor/a": {Existing: Existing{Kind: "monitor", ID: "a", Digest: "old", Revision: 1}, Payload: json.RawMessage(`{"x":1}`), Reversible: false},
		"monitor/b": {Existing: Existing{Kind: "monitor", ID: "b", Digest: "old", Revision: 1}, Payload: json.RawMessage(`{"y":1}`), Reversible: true},
	}, fail: "c"}
	m := Manager{Adapter: a, Store: st, Now: func() time.Time { return time.Unix(1, 0) }}
	src := []Envelope{
		{Kind: "monitor", ID: "a", SchemaVersion: 1, Revision: 2, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"x":2}`)},
		{Kind: "monitor", ID: "b", SchemaVersion: 1, Revision: 2, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"y":2}`)},
		{Kind: "monitor", ID: "c", SchemaVersion: 1, Revision: 1, SourceNode: "n", Target: "local", Conflict: ConflictSourceWins, Actor: Actor{Permission: "monitor.update"}, Payload: json.RawMessage(`{"z":1}`)},
	}
	for i := range src {
		if err := src[i].Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Apply(context.Background(), "plan-1", src, Actor{Permission: "monitor.update"}, "node-a"); err == nil {
		t.Fatal("expected apply failure")
	}
	if len(st.history) != 1 {
		t.Fatalf("expected one history record, got %d", len(st.history))
	}
	h := st.history[0]
	if h.Applied != 2 {
		t.Fatalf("expected applied=2, got %d", h.Applied)
	}
	if h.RolledBack != 1 {
		t.Fatalf("expected rolled_back=1, got %d", h.RolledBack)
	}
	if h.Failed != 1 {
		t.Fatalf("expected failed=1, got %d", h.Failed)
	}
}
