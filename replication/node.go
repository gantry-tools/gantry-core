package replication

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

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
	Bootstrap     bool    // bootstrap this node as the sole voter (empty store only)

	HeartbeatTimeout   time.Duration // zero => defaults
	ElectionTimeout    time.Duration
	CommitTimeout      time.Duration
	LeaderLeaseTimeout time.Duration
	ProposeTimeout     time.Duration // zero => 5s

	Logger io.Writer
}

// Node wraps a consensus node (hashicorp/raft) plus the deterministic FSM and
// the Gantry replicated-operation contract (proposal, acknowledgement,
// idempotency, follower forwarding).
type Node struct {
	id      raft.ServerID
	addr    raft.ServerAddress
	raft    *raft.Raft
	fsm     *KVFSM
	fabric  *Fabric
	timeout time.Duration
}

// NewNode constructs a replicated-state node. Bootstrap is only applied when
// the supplied stores have no existing state, so restarting a node never
// re-bootstraps an existing cluster.
func NewNode(opts NodeOptions) (*Node, error) {
	if opts.LogStore == nil || opts.StableStore == nil || opts.SnapshotStore == nil || opts.Transport == nil {
		return nil, fmt.Errorf("replication: log/stable/snapshot stores and transport are required")
	}
	fsm := NewKVFSM()

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
	return &Node{id: opts.ID, addr: opts.Address, raft: r, fsm: fsm, fabric: opts.Fabric, timeout: timeout}, nil
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
func (n *Node) FSM() *KVFSM { return n.fsm }

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
	return n.proposeLocal(ctx, op)
}

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
