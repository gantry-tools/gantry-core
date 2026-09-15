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
	product      string
	state        map[string]kvEntry
	applied      map[string]appliedOp
	appliedIndex uint64
	appliedTerm  uint64
}

// NewKVFSM returns a KVFSM supporting the current build's replication version.
func NewKVFSM() *KVFSM { return NewKVFSMWithSchema(Version) }

// NewKVFSMWithSchema returns a KVFSM whose apply/snapshot gates accept
// operation schemas up to maxSchema (used to simulate rolling-version nodes).
func NewKVFSMWithSchema(maxSchema int) *KVFSM {
	if maxSchema < 1 {
		maxSchema = 1
	}
	if maxSchema > Version {
		maxSchema = Version
	}
	return &KVFSM{
		supported: maxSchema,
		state:     make(map[string]kvEntry),
		applied:   make(map[string]appliedOp),
	}
}

// SetSupported raises/lowers the operation schema versions this FSM accepts
// (models a rolling software upgrade on a live node).
func (f *KVFSM) SetSupported(maxSchema int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if maxSchema < 1 {
		maxSchema = 1
	}
	if maxSchema > Version {
		maxSchema = Version
	}
	f.supported = maxSchema
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
	if op.Version > f.supported {
		return fmt.Errorf("node does not support replication operation version %d (supported: 1..%d)", op.Version, f.supported)
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
	if f.product == "" {
		f.product = op.Product
	}
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
// by construction. Persist writes the versioned semantic SnapshotEnvelope.
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
	return &kvSnapshot{product: f.product, supported: f.supported, state: state, applied: applied, index: f.appliedIndex, term: f.appliedTerm}, nil
}

// Restore implements raft.FSM. It is atomic from the semantic state machine's
// perspective: the snapshot is decoded and integrity-verified, format and
// replication versions are validated, and a replacement state is constructed
// before any existing state is touched. An incompatible or corrupted snapshot
// is rejected before it can partially mutate the FSM.
func (f *KVFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	env, err := DecodeSnapshot(b)
	if err != nil {
		return err
	}
	if err := env.validate(); err != nil {
		return err
	}
	if env.ReplicationVersion > f.supported {
		return fmt.Errorf("node does not support snapshot replication version %d (supported: 1..%d)", env.ReplicationVersion, f.supported)
	}

	state := make(map[string]kvEntry, len(env.State))
	for k, v := range env.State {
		state[k] = v
	}
	applied := make(map[string]appliedOp, len(env.AppliedOps))
	for k, v := range env.AppliedOps {
		applied[k] = v
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.product = env.Product
	f.state = state
	f.applied = applied
	f.appliedIndex = env.RaftIndex
	f.appliedTerm = env.RaftTerm
	return nil
}

type kvSnapshot struct {
	product   string
	supported int
	state     map[string]kvEntry
	applied   map[string]appliedOp
	index     uint64
	term      uint64
	released  bool
}

var _ raft.FSMSnapshot = (*kvSnapshot)(nil)

// Persist writes the canonical SnapshotEnvelope (JSON map keys are sorted by
// encoding/json, so equal semantic state always produces equal bytes). The
// replication version recorded is the producing node's supported schema max.
func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
	env := SnapshotEnvelope{
		FormatVersion:      SnapshotFormatVersion,
		ReplicationVersion: s.supported,
		Product:            s.product,
		RaftIndex:          s.index,
		RaftTerm:           s.term,
		State:              s.state,
		AppliedOps:         s.applied,
	}
	b, err := env.Encode()
	if err != nil {
		_ = sink.Cancel()
		return err
	}
	if _, err := sink.Write(b); err != nil {
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
