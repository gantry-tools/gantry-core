package replication

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/raft"
)

func kvSet(id, key, value string, rev int64) Operation {
	return Operation{
		ID: id, Product: "watchpost", Version: Version, Kind: KindSet,
		ObjectKind: "key", ObjectID: key, Revision: rev,
		Payload:    json.RawMessage(`{"value":"` + value + `"}`),
		OriginNode: "n1",
	}
}

func kvDelete(id, key string, rev int64) Operation {
	return Operation{
		ID: id, Product: "watchpost", Version: Version, Kind: KindDelete,
		ObjectKind: "key", ObjectID: key, Revision: rev,
		Payload:    json.RawMessage(`{}`),
		OriginNode: "n1",
	}
}

func applyLog(t *testing.T, f *KVFSM, ops []Operation) {
	t.Helper()
	for i, op := range ops {
		b, err := op.Encode()
		if err != nil {
			t.Fatal(err)
		}
		res := f.Apply(&raft.Log{Index: uint64(i + 1), Term: 1, Data: b})
		if e, ok := res.(error); ok {
			t.Fatalf("apply op %q: %v", op.ID, e)
		}
	}
}

func TestKVFSMDeterministicReplay(t *testing.T) {
	ops := []Operation{
		kvSet("op-1", "foo", "A", 0),
		kvSet("op-2", "foo", "B", 1), // precondition on revision 1
		kvSet("op-3", "bar", "X", 0),
		kvDelete("op-4", "foo", 2),
		kvSet("op-5", "baz", "Z", 0),
	}
	a, b := NewKVFSM(), NewKVFSM()
	applyLog(t, a, ops)
	applyLog(t, b, ops)

	for _, key := range []string{"foo", "bar", "baz"} {
		va, ra, oka := a.Get(key)
		vb, rb, okb := b.Get(key)
		if oka != okb || va != vb || ra != rb {
			t.Fatalf("replicas diverged on %q: (%v,%q,%d) vs (%v,%q,%d)", key, oka, va, ra, okb, vb, rb)
		}
	}
	ia, ta := a.AppliedIndex()
	ib, tb := b.AppliedIndex()
	if ia != ib || ta != tb {
		t.Fatalf("applied position diverged: (%d,%d) vs (%d,%d)", ia, ta, ib, tb)
	}

	// A third replica: apply first half, snapshot, restore, then replay the rest.
	c := NewKVFSM()
	applyLog(t, c, ops[:2])
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := snap.Persist(snapshotSink{&buf}); err != nil {
		t.Fatal(err)
	}
	d := NewKVFSM()
	if err := d.Restore(snapshotReader{bytes.NewReader(buf.Bytes())}); err != nil {
		t.Fatal(err)
	}
	applyLog(t, d, ops[2:])
	for _, key := range []string{"foo", "bar", "baz"} {
		va, _, oka := a.Get(key)
		vd, _, okd := d.Get(key)
		if oka != okd || va != vd {
			t.Fatalf("snapshot-restore replica diverged on %q: %v vs %v", key, va, vd)
		}
	}
}

func TestKVFSMIdempotency(t *testing.T) {
	f := NewKVFSM()
	op := kvSet("op-1", "foo", "A", 0)
	b, _ := op.Encode()
	first := f.Apply(&raft.Log{Index: 1, Term: 1, Data: b}).(*ApplyResult)
	second := f.Apply(&raft.Log{Index: 2, Term: 1, Data: b}).(*ApplyResult)
	if first.Revision != 1 || second.Revision != 1 {
		t.Fatalf("retry changed revision: first=%d second=%d", first.Revision, second.Revision)
	}
	if second.Index != first.Index {
		t.Fatalf("retry reported a different committed index: %d vs %d", second.Index, first.Index)
	}
	v, rev, ok := f.Get("foo")
	if !ok || v != "A" || rev != 1 {
		t.Fatalf("unexpected state after idempotent retry: %q rev=%d ok=%v", v, rev, ok)
	}
}

func TestKVFSMSameIDDifferentPayloadFailsClosed(t *testing.T) {
	f := NewKVFSM()
	op := kvSet("op-1", "foo", "A", 0)
	b, _ := op.Encode()
	if res := f.Apply(&raft.Log{Index: 1, Term: 1, Data: b}); isErr(res) {
		t.Fatalf("first apply failed: %v", res)
	}
	other := kvSet("op-1", "foo", "B", 0) // same ID, different payload
	b2, _ := other.Encode()
	res := f.Apply(&raft.Log{Index: 2, Term: 1, Data: b2})
	if !isErr(res) {
		t.Fatal("same ID with different payload must fail closed")
	}
	if !strings.Contains(res.(error).Error(), "different payload") {
		t.Fatalf("unexpected error: %v", res)
	}
}

func TestKVFSMStalePreconditionRejected(t *testing.T) {
	f := NewKVFSM()
	applyLog(t, f, []Operation{kvSet("op-1", "foo", "A", 0)})
	// stale precondition revision 5
	op := kvSet("op-2", "foo", "B", 5)
	b, _ := op.Encode()
	res := f.Apply(&raft.Log{Index: 2, Term: 1, Data: b})
	if !isErr(res) {
		t.Fatal("stale mutation must be rejected")
	}
	v, rev, _ := f.Get("foo")
	if v != "A" || rev != 1 {
		t.Fatalf("state mutated despite rejection: %q rev=%d", v, rev)
	}
}

func TestKVFSMSnapshotVersionFailClosed(t *testing.T) {
	f := NewKVFSM()
	applyLog(t, f, []Operation{kvSet("op-1", "foo", "A", 0)})
	snap, err := f.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := snap.Persist(snapshotSink{&buf}); err != nil {
		t.Fatal(err)
	}
	valid := buf.Bytes()

	// Producer gate: an unsupported replication version cannot be encoded.
	env, err := DecodeSnapshot(valid)
	if err != nil {
		t.Fatal(err)
	}
	env.ReplicationVersion = Version + 1
	if _, err := env.Encode(); err == nil || !strings.Contains(err.Error(), "unsupported snapshot replication version") {
		t.Fatalf("Encode must reject an unsupported replication version, got %v", err)
	}

	// Restore gate (format version): a valid-integrity envelope carrying an
	// unsupported format version must be rejected before any state is touched.
	raw := &SnapshotEnvelope{
		FormatVersion:      SnapshotFormatVersion + 1,
		ReplicationVersion: Version,
		State:              map[string]kvEntry{"x": {Value: "y", Revision: 1}},
		AppliedOps:         map[string]appliedOp{},
	}
	d, err := raw.contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	raw.Integrity = d
	badFormat, _ := json.Marshal(raw)
	f2 := NewKVFSM()
	applyLog(t, f2, []Operation{kvSet("op-existing", "keep", "K", 0)})
	if err := f2.Restore(snapshotReader{bytes.NewReader(badFormat)}); err == nil || !strings.Contains(err.Error(), "unsupported snapshot format version") {
		t.Fatalf("Restore must reject an unsupported format version, got %v", err)
	}
	if v, _, ok := f2.Get("keep"); !ok || v != "K" {
		t.Fatal("a rejected snapshot must not mutate existing state")
	}

	// Corruption: tampering the payload breaks integrity and fails closed.
	corrupted := append([]byte(nil), valid...)
	corrupted[len(corrupted)/2] ^= 0xff
	f3 := NewKVFSM()
	applyLog(t, f3, []Operation{kvSet("op-existing", "keep", "K", 0)})
	if err := f3.Restore(snapshotReader{bytes.NewReader(corrupted)}); err == nil {
		t.Fatal("a corrupted snapshot must fail closed")
	}
	if v, _, ok := f3.Get("keep"); !ok || v != "K" {
		t.Fatal("a corrupted snapshot must not mutate existing state")
	}
}

func TestKVFSMUnsupportedKindRejected(t *testing.T) {
	injected, err := json.Marshal(Operation{
		ID: "op-1", Product: "watchpost", Version: Version, Kind: "sql",
		ObjectKind: "key", ObjectID: "foo", Payload: json.RawMessage(`{"value":"A"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	f := NewKVFSM()
	res := f.Apply(&raft.Log{Index: 1, Term: 1, Data: injected})
	if !isErr(res) {
		t.Fatal("unsupported kind must be rejected during apply")
	}
}

func TestKVFSMVersionFailClosed(t *testing.T) {
	// Proposal gate: an unsupported version is rejected before it enters the log.
	op := kvSet("op-1", "foo", "A", 0)
	op.Version = Version + 1
	if _, err := op.Encode(); err == nil || !strings.Contains(err.Error(), "unsupported replication operation version") {
		t.Fatalf("proposal gate did not reject unsupported version: %v", err)
	}

	// Defense in depth: even if an unsupported-version entry reached the log,
	// apply must reject it deterministically without mutating state.
	injected, err := json.Marshal(Operation{
		ID: "op-1", Product: "watchpost", Version: Version + 1, Kind: KindSet,
		ObjectKind: "key", ObjectID: "foo", Payload: json.RawMessage(`{"value":"A"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	f := NewKVFSM()
	res := f.Apply(&raft.Log{Index: 1, Term: 1, Data: injected})
	if !isErr(res) {
		t.Fatal("unsupported version must be rejected during apply")
	}
	if _, _, ok := f.Get("foo"); ok {
		t.Fatal("rejected operation must not mutate state")
	}
}

func isErr(v interface{}) bool {
	_, ok := v.(error)
	return ok
}

type snapshotSink struct{ buf *bytes.Buffer }

func (s snapshotSink) ID() string                  { return "sink" }
func (s snapshotSink) Cancel() error               { return nil }
func (s snapshotSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s snapshotSink) Close() error                { return nil }

type snapshotReader struct{ r *bytes.Reader }

func (r snapshotReader) Read(p []byte) (int, error) { return r.r.Read(p) }
func (r snapshotReader) Close() error               { return nil }
