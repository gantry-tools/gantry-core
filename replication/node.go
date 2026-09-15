package replication

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Capabilities describes what a node understands semantically. Compatibility
// is never inferred from application version strings; it is negotiated from
// these explicit supported-version sets.
type Capabilities struct {
	ID                      raft.ServerID `json:"id"`
	OperationSchemaVersions []int         `json:"operation_schema_versions"`
	SnapshotFormatVersions  []int         `json:"snapshot_format_versions"`
	Features                []string      `json:"features,omitempty"`
}

// SupportsOperation reports whether the node understands replication schema v.
func (c Capabilities) SupportsOperation(v int) bool {
	for _, s := range c.OperationSchemaVersions {
		if s == v {
			return true
		}
	}
	return false
}

// SupportsSnapshotFormat reports whether the node understands snapshot format f.
func (c Capabilities) SupportsSnapshotFormat(f int) bool {
	for _, s := range c.SnapshotFormatVersions {
		if s == f {
			return true
		}
	}
	return false
}

// StateMachine is the deterministic replicated state machine a Node drives.
// KVFSM is the reference/proving implementation; products provide their own to
// materialize committed Operations into product state. It extends raft.FSM
// with the durable operation-ID and applied-position accessors the
// proposal/acknowledgement contract needs.
type StateMachine interface {
	raft.FSM
	// OpKnown reports whether an operation ID has been applied and its digest.
	OpKnown(id string) (string, bool)
	// AppliedIndex returns the last applied raft index and term.
	AppliedIndex() (uint64, uint64)
}

var _ StateMachine = (*KVFSM)(nil)

// NodeOptions configures a replicated-state node. The durable/log and
// snapshot stores are injectable so tests can run in-memory or durable.
type NodeOptions struct {
	ID            raft.ServerID
	Address       raft.ServerAddress
	Transport     raft.Transport
	LogStore      raft.LogStore
	StableStore   raft.StableStore
	SnapshotStore raft.SnapshotStore
	Fabric        *Fabric // nil for standalone
	// FSM is the deterministic product state machine. Nil uses the KVFSM
	// reference implementation.
	FSM       StateMachine
	Bootstrap bool // bootstrap this node as the sole voter (empty store only)

	HeartbeatTimeout   time.Duration // zero => defaults
	ElectionTimeout    time.Duration
	CommitTimeout      time.Duration
	LeaderLeaseTimeout time.Duration
	ProposeTimeout     time.Duration // zero => 5s

	// SupportedReplicationVersion caps the operation schema versions this node
	// advertises (default: the build's Version). SupportedSnapshotFormat is the
	// snapshot format version (default SnapshotFormatVersion).
	SupportedReplicationVersion int
	SupportedSnapshotFormat     int
	Features                    []string

	// CapabilitySource supplies authenticated voter capabilities for schema
	// gating. Nil defaults to the Fabric (test harness); production supplies a
	// membership-backed source.
	CapabilitySource CapabilitySource

	Logger io.Writer
}

// Node wraps a consensus node (hashicorp/raft) plus the deterministic FSM and
// the Gantry replicated-operation contract (proposal, acknowledgement,
// idempotency, follower forwarding).
type Node struct {
	id      raft.ServerID
	addr    raft.ServerAddress
	raft    *raft.Raft
	fsm     StateMachine
	fabric  *Fabric
	timeout time.Duration
	caps    Capabilities
	capSrc  CapabilitySource

	mu    sync.Mutex
	local map[string]string
}

// NewNode constructs a replicated-state node. Bootstrap is only applied when
// the supplied stores have no existing state, so restarting a node never
// re-bootstraps an existing cluster.
func NewNode(opts NodeOptions) (*Node, error) {
	if opts.LogStore == nil || opts.StableStore == nil || opts.SnapshotStore == nil || opts.Transport == nil {
		return nil, fmt.Errorf("replication: log/stable/snapshot stores and transport are required")
	}
	maxSchema := opts.SupportedReplicationVersion
	if maxSchema <= 0 {
		maxSchema = Version
	}
	if maxSchema > Version {
		maxSchema = Version
	}
	fsm := opts.FSM
	if fsm == nil {
		fsm = NewKVFSMWithSchema(maxSchema)
	}

	conf := raft.DefaultConfig()
	conf.LocalID = opts.ID
	if opts.Logger != nil {
		conf.Logger = hclog.New(&hclog.LoggerOptions{Name: string(opts.ID), Output: opts.Logger})
	} else {
		conf.Logger = hclog.New(&hclog.LoggerOptions{Name: string(opts.ID), Output: io.Discard})
	}
	if opts.HeartbeatTimeout > 0 {
		conf.HeartbeatTimeout = opts.HeartbeatTimeout
	}
	if opts.ElectionTimeout > 0 {
		conf.ElectionTimeout = opts.ElectionTimeout
	}
	if opts.CommitTimeout > 0 {
		conf.CommitTimeout = opts.CommitTimeout
	}
	if opts.LeaderLeaseTimeout > 0 {
		conf.LeaderLeaseTimeout = opts.LeaderLeaseTimeout
	}
	// Snapshots are triggered manually for the deterministic harness; automatic
	// snapshotting would introduce timing-dependent behaviour.
	conf.SnapshotInterval = 24 * time.Hour
	conf.SnapshotThreshold = 1 << 30
	conf.TrailingLogs = 8
	conf.ShutdownOnRemove = false

	if opts.Bootstrap {
		has, err := raft.HasExistingState(opts.LogStore, opts.StableStore, opts.SnapshotStore)
		if err != nil {
			return nil, err
		}
		if !has {
			cfg := raft.Configuration{Servers: []raft.Server{{ID: opts.ID, Address: opts.Address, Suffrage: raft.Voter}}}
			if err := raft.BootstrapCluster(conf, opts.LogStore, opts.StableStore, opts.SnapshotStore, opts.Transport, cfg); err != nil {
				return nil, fmt.Errorf("replication: bootstrap: %w", err)
			}
		}
	}

	r, err := raft.NewRaft(conf, fsm, opts.LogStore, opts.StableStore, opts.SnapshotStore, opts.Transport)
	if err != nil {
		return nil, err
	}
	timeout := opts.ProposeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	opSchema := make([]int, 0, maxSchema)
	for v := 1; v <= maxSchema; v++ {
		opSchema = append(opSchema, v)
	}
	snapFormat := opts.SupportedSnapshotFormat
	if snapFormat <= 0 {
		snapFormat = SnapshotFormatVersion
	}
	caps := Capabilities{
		ID:                      opts.ID,
		OperationSchemaVersions: opSchema,
		SnapshotFormatVersions:  []int{snapFormat},
		Features:                opts.Features,
	}
	if opts.Fabric != nil {
		opts.Fabric.RegisterCapabilities(opts.ID, caps)
	}
	capSrc := opts.CapabilitySource
	if capSrc == nil {
		capSrc = opts.Fabric
	}
	return &Node{id: opts.ID, addr: opts.Address, raft: r, fsm: fsm, fabric: opts.Fabric, timeout: timeout, caps: caps, capSrc: capSrc, local: make(map[string]string)}, nil
}

// ID returns the node's stable identity.
func (n *Node) ID() raft.ServerID { return n.id }

// Address returns the node's transport address.
func (n *Node) Address() raft.ServerAddress { return n.addr }

// State returns the current raft state.
func (n *Node) State() raft.RaftState { return n.raft.State() }

// Stats returns the underlying raft statistics map (used by the harness).
func (n *Node) Stats() map[string]string { return n.raft.Stats() }

// Leader returns the current leader address and id (empty when none).
func (n *Node) Leader() (raft.ServerAddress, raft.ServerID) { return n.raft.LeaderWithID() }

// FSM exposes the deterministic state machine (used by the harness to assert
// replicated state).
func (n *Node) FSM() StateMachine { return n.fsm }

// LastIndex returns the highest index in the node's log (after snapshots).
func (n *Node) LastIndex() uint64 { return n.raft.LastIndex() }

// AppliedIndex returns the highest applied raft index and term.
func (n *Node) AppliedIndex() (uint64, uint64) { return n.fsm.AppliedIndex() }

// Propose submits an operation to the leader. The acknowledgement contract is:
//
//	proposed != committed != responded
//	success response => quorum-committed AND applied on the leader
//
// A retried operation ID resolves to the already-committed result; a retried
// ID carrying a different payload fails closed before proposal.
//
// A schema-N operation is only proposal-eligible when every voting member of
// the current raft configuration advertises support for schema N (the
// leader-side half of the rolling-version rule; the FSM apply gate is the
// deterministic half).
func (n *Node) Propose(ctx context.Context, op Operation) (*ApplyResult, error) {
	if digest, ok := n.fsm.OpKnown(op.ID); ok {
		d, err := op.Digest()
		if err != nil {
			return nil, err
		}
		if d != digest {
			return nil, fmt.Errorf("operation id %q already applied with a different payload; refusing", op.ID)
		}
	}
	if n.raft.State() == raft.Leader {
		if err := n.requireVoterSchemaSupport(op.Version); err != nil {
			return nil, err
		}
	}
	return n.proposeLocal(ctx, op)
}

// requireVoterSchemaSupport enforces the rolling-version rule: operations at
// schema N are not eligible to commit while any current voter cannot
// understand schema N. Learners/non-voters are excluded from the quorum
// contract and therefore do not gate schema activation.
func (n *Node) requireVoterSchemaSupport(v int) error {
	if !n.caps.SupportsOperation(v) {
		return fmt.Errorf("node %s does not support replication schema %d", n.id, v)
	}
	if n.capSrc == nil {
		return nil
	}
	cfg, err := n.Configuration()
	if err != nil {
		return err
	}
	for _, s := range cfg {
		if s.Suffrage != raft.Voter {
			continue
		}
		caps, ok := n.capSrc.CapabilitiesOf(s.ID)
		if !ok {
			return fmt.Errorf("voter %s capabilities unknown; failing closed", s.ID)
		}
		if !caps.SupportsOperation(v) {
			return fmt.Errorf("voter %s does not support replication schema %d; schema activation blocked", s.ID, v)
		}
	}
	return nil
}

// SetLocal stores a node-local value that must never be replicated. It models
// node identity, peer credentials, pairing state, nonces, sessions, telemetry
// and scheduler execution state, all of which are excluded from snapshots by
// construction.
func (n *Node) SetLocal(key, val string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.local[key] = val
}

// GetLocal reads a node-local value.
func (n *Node) GetLocal(key string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.local[key]
	return v, ok
}

// LocalKeys returns the node-local keys (for exclusion assertions).
func (n *Node) LocalKeys() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	keys := make([]string, 0, len(n.local))
	for k := range n.local {
		keys = append(keys, k)
	}
	return keys
}

// Capabilities returns the node's advertised replication capabilities.
func (n *Node) Capabilities() Capabilities { return n.caps }

// SetSupportedReplicationVersion models a rolling software upgrade on a live
// node: it raises (or lowers) the operation schemas the node advertises and
// the FSM will accept, re-registering capabilities for schema negotiation.
func (n *Node) SetSupportedReplicationVersion(max int) {
	if max < 1 {
		max = 1
	}
	if max > Version {
		max = Version
	}
	opSchema := make([]int, 0, max)
	for v := 1; v <= max; v++ {
		opSchema = append(opSchema, v)
	}
	n.caps.OperationSchemaVersions = opSchema
	if kf, ok := n.fsm.(*KVFSM); ok {
		kf.SetSupported(max)
	}
	if n.fabric != nil {
		n.fabric.RegisterCapabilities(n.id, n.caps)
	}
}

// ExportSnapshot returns the canonical filtered logical snapshot of the
// replicated state. Node-local state never appears in it.
func (n *Node) ExportSnapshot() ([]byte, error) {
	snap, err := n.fsm.Snapshot()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := snap.Persist(&memorySnapshotSink{&buf}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ApplySnapshot atomically installs a filtered logical snapshot into the FSM.
// In production this path is exercised by raft's own snapshot installation
// during catch-up; the direct form exists for deterministic harness proof.
func (n *Node) ApplySnapshot(b []byte) error {
	return n.fsm.Restore(io.NopCloser(bytes.NewReader(b)))
}

type memorySnapshotSink struct{ buf *bytes.Buffer }

func (s *memorySnapshotSink) ID() string                  { return "memory" }
func (s *memorySnapshotSink) Cancel() error               { return nil }
func (s *memorySnapshotSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *memorySnapshotSink) Close() error                { return nil }

// proposeLocal proposes on this node, which must be the leader.
func (n *Node) proposeLocal(ctx context.Context, op Operation) (*ApplyResult, error) {
	if n.raft.State() != raft.Leader {
		return nil, ErrNotLeader
	}
	data, err := op.Encode()
	if err != nil {
		return nil, err
	}
	fut := n.raft.Apply(data, n.timeout)
	if err := fut.Error(); err != nil {
		return nil, err
	}
	resp := fut.Response()
	if e, ok := resp.(error); ok {
		return nil, e
	}
	res, ok := resp.(*ApplyResult)
	if !ok {
		return nil, fmt.Errorf("replication: unexpected apply response %T", resp)
	}
	return res, nil
}

// handlePropose is the entry point the Cluster/fabric routing uses: a leader
// proposes locally; a follower forwards the operation to the current leader
// through the fault-injectable fabric (respecting partitions).
func (n *Node) handlePropose(ctx context.Context, op Operation) (*ApplyResult, error) {
	if n.raft.State() == raft.Leader {
		return n.proposeLocal(ctx, op)
	}
	if n.fabric == nil {
		return nil, ErrNotLeader
	}
	_, leaderID := n.raft.LeaderWithID()
	if leaderID == "" {
		return nil, ErrNotLeader
	}
	return n.fabric.ForwardProposal(n.id, leaderID, op, n.timeout)
}

// Barrier flushes committed entries through the local FSM.
func (n *Node) Barrier() error { return n.raft.Barrier(n.timeout).Error() }

// AddVoter adds a voting member (leader required).
func (n *Node) AddVoter(id raft.ServerID, addr raft.ServerAddress) error {
	return n.raft.AddVoter(id, addr, 0, n.timeout).Error()
}

// AddNonvoter adds a learner/non-voting replica (leader required).
func (n *Node) AddNonvoter(id raft.ServerID, addr raft.ServerAddress) error {
	return n.raft.AddNonvoter(id, addr, 0, n.timeout).Error()
}

// RemoveServer removes a member (leader required).
func (n *Node) RemoveServer(id raft.ServerID) error {
	return n.raft.RemoveServer(id, 0, n.timeout).Error()
}

// DemoteVoter demotes a voter to non-voter (leader required).
func (n *Node) DemoteVoter(id raft.ServerID) error {
	return n.raft.DemoteVoter(id, 0, n.timeout).Error()
}

// LeadershipTransfer hands leadership to a follower (leader required).
func (n *Node) LeadershipTransfer() error { return n.raft.LeadershipTransfer().Error() }

// Configuration returns the current raft configuration (servers and suffrage).
func (n *Node) Configuration() ([]raft.Server, error) {
	fut := n.raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		return nil, err
	}
	return fut.Configuration().Servers, nil
}

// Snapshot triggers a manual snapshot of the FSM.
func (n *Node) Snapshot() error { return n.raft.Snapshot().Error() }

// Shutdown stops the node cleanly.
func (n *Node) Shutdown() error { return n.raft.Shutdown().Error() }
