package replication

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"
)

// ApplyResult is the deterministic outcome of applying a committed operation.
// A retried operation ID returns the original ApplyResult unchanged.
type ApplyResult struct {
	Index    uint64 `json:"index"`
	Term     uint64 `json:"term"`
	OpID     string `json:"op_id"`
	ObjectID string `json:"object_id"`
	Kind     string `json:"kind"`
	Version  int    `json:"version"`
	Revision int64  `json:"revision"`
}

type kvEntry struct {
	Value    string `json:"value"`
	Revision int64  `json:"revision"`
}

type appliedOp struct {
	Digest string       `json:"digest"`
	Result *ApplyResult `json:"result"`
}

// KVFSM is the product-independent deterministic state machine used to prove
// the replicated-state machinery (Phase 7B). Every voting replica applying the
// same committed log must reach byte/semantically identical state.
//
// It deliberately holds only replicated keyspace; node-local state (identity,
// credentials, sessions, nonces, telemetry) is never part of it, so filtered
// logical snapshots exclude it by construction.
type KVFSM struct {
	mu           sync.Mutex
	supported    int
	state        map[string]kvEntry
	applied      map[string]appliedOp
	appliedIndex uint64
	appliedTerm  uint64
}

// NewKVFSM returns a KVFSM supporting the current replication version.
func NewKVFSM() *KVFSM {
	return &KVFSM{
		supported: Version,
		state:     make(map[string]kvEntry),
		applied:   make(map[string]appliedOp),
	}
}

var _ raft.FSM = (*KVFSM)(nil)

// Apply implements raft.FSM. It is deterministic and idempotent: a repeated
// operation ID returns the original result without mutating state, and a
// repeated ID with a different payload fails closed.
func (f *KVFSM) Apply(l *raft.Log) interface{} {
	op, err := DecodeOperation(l.Data)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applyLocked(op, l.Index, l.Term)
}

func (f *KVFSM) applyLocked(op Operation, index, term uint64) interface{} {
	if !SupportedVersion(op.Version) {
		return fmt.Errorf("unsupported replication operation version %d (supported: 1..%d)", op.Version, f.supported)
	}
	if err := ValidateKind(op.Kind); err != nil {
		return err
	}
	digest, err := op.Digest()
	if err != nil {
		return err
	}
	if prior, ok := f.applied[op.ID]; ok {
		if prior.Digest != digest {
			return fmt.Errorf("operation id %q retried with a different payload; refusing", op.ID)
		}
		return prior.Result
	}

	var newRev int64
	switch op.Kind {
	case KindSet:
		var kv struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(op.Payload, &kv); err != nil {
			return err
		}
		cur, exists := f.state[op.ObjectID]
		if op.Revision != 0 {
			if !exists || cur.Revision != op.Revision {
				return fmt.Errorf("stale mutation for %q: precondition revision %d does not match current %d", op.ObjectID, op.Revision, cur.Revision)
			}
		}
		newRev = cur.Revision + 1
		f.state[op.ObjectID] = kvEntry{Value: kv.Value, Revision: newRev}
	case KindDelete:
		cur, exists := f.state[op.ObjectID]
		if op.Revision != 0 && (!exists || cur.Revision != op.Revision) {
			return fmt.Errorf("stale mutation for %q: precondition revision %d does not match current %d", op.ObjectID, op.Revision, cur.Revision)
		}
		newRev = cur.Revision + 1
		delete(f.state, op.ObjectID)
	default:
		return fmt.Errorf("unsupported operation kind %q", op.Kind)
	}

	res := &ApplyResult{
		Index:    index,
		Term:     term,
		OpID:     op.ID,
		ObjectID: op.ObjectID,
		Kind:     op.Kind,
		Version:  op.Version,
		Revision: newRev,
	}
	f.applied[op.ID] = appliedOp{Digest: digest, Result: res}
	f.appliedIndex = index
	f.appliedTerm = term
	return res
}

// OpKnown reports whether an operation ID has already been applied, and its
// canonical digest. Used by the leader to reject a same-ID/different-payload
// retry before proposing.
func (f *KVFSM) OpKnown(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.applied[id]
	if !ok {
		return "", false
	}
	return p.Digest, true
}

// Get returns the current replicated value, revision and presence.
func (f *KVFSM) Get(key string) (string, int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.state[key]
	if !ok {
		return "", 0, false
	}
	return e.Value, e.Revision, true
}

// AppliedIndex returns the last applied raft index and term.
func (f *KVFSM) AppliedIndex() (uint64, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.appliedIndex, f.appliedTerm
}

// Snapshot implements raft.FSM. It returns the replicated keyspace plus the
// durable idempotency record and applied position; node-local state is absent
// by construction.
func (f *KVFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := make(map[string]kvEntry, len(f.state))
	for k, v := range f.state {
		state[k] = v
	}
	applied := make(map[string]appliedOp, len(f.applied))
	for k, v := range f.applied {
		applied[k] = v
	}
	return &kvSnapshot{state: state, applied: applied, index: f.appliedIndex, term: f.appliedTerm}, nil
}

// Restore implements raft.FSM: the FSM discards all previous state and loads
// the snapshot.
func (f *KVFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var snap snapshotState
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}
	if !SupportedVersion(snap.Version) {
		return fmt.Errorf("unsupported snapshot replication version %d (supported: 1..%d)", snap.Version, f.supported)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = snap.State
	if f.state == nil {
		f.state = make(map[string]kvEntry)
	}
	f.applied = snap.AppliedOps
	if f.applied == nil {
		f.applied = make(map[string]appliedOp)
	}
	f.appliedIndex = snap.AppliedIndex
	f.appliedTerm = snap.AppliedTerm
	return nil
}

type snapshotState struct {
	Version      int                  `json:"version"`
	State        map[string]kvEntry   `json:"state"`
	AppliedOps   map[string]appliedOp `json:"applied_ops"`
	AppliedIndex uint64               `json:"applied_index"`
	AppliedTerm  uint64               `json:"applied_term"`
}

type kvSnapshot struct {
	state    map[string]kvEntry
	applied  map[string]appliedOp
	index    uint64
	term     uint64
	released bool
}

var _ raft.FSMSnapshot = (*kvSnapshot)(nil)

// Persist writes the canonical snapshot (JSON map keys are sorted by
// encoding/json, so the bytes are deterministic).
func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
	enc := json.NewEncoder(sink)
	if err := enc.Encode(snapshotState{Version: Version, State: s.state, AppliedOps: s.applied, AppliedIndex: s.index, AppliedTerm: s.term}); err != nil {
		_ = sink.Cancel()
		return err
	}
	if err := sink.Close(); err != nil {
		return err
	}
	return nil
}

func (s *kvSnapshot) Release() {
	s.released = true
}

// ErrNotLeader reports that the node is not the current leader.
var ErrNotLeader = errors.New("replication: not leader")
