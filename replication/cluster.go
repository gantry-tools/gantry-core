package replication

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
)

// Cluster is a deterministic in-process multi-node replicated-state harness
// (Phase 7B). Nodes share one Fabric so partitions, isolation, leader loss,
// quorum loss and stale-leader scenarios can be injected deterministically.
// No real network is used.
type Cluster struct {
	Fabric *Fabric
	Nodes  map[raft.ServerID]*Node

	stores    map[raft.ServerID]*BoltStore
	heartbeat time.Duration
	election  time.Duration
	commit    time.Duration
	lease     time.Duration
	propose   time.Duration
}

// NewCluster returns an empty harness cluster with fast deterministic timings.
func NewCluster() *Cluster {
	return &Cluster{
		Fabric:    NewFabric(),
		Nodes:     make(map[raft.ServerID]*Node),
		stores:    make(map[raft.ServerID]*BoltStore),
		heartbeat: 100 * time.Millisecond,
		election:  200 * time.Millisecond,
		commit:    20 * time.Millisecond,
		lease:     100 * time.Millisecond,
		propose:   3 * time.Second,
	}
}

// AddNode creates a node with default capabilities. The first node is
// bootstrapped as the sole voter. When durable is true the node persists raft
// term/vote state and the log to dir (bbolt) and snapshots to dir/snapshots
// (file store); when false the log and stable state are in-memory while
// snapshots remain file-backed so snapshot install can still be exercised.
func (c *Cluster) AddNode(id raft.ServerID, dir string, durable bool) (*Node, error) {
	return c.AddNodeCaps(id, dir, durable, Version, SnapshotFormatVersion, nil)
}

// AddNodeCaps is AddNode with explicit capability overrides, used to exercise
// schema negotiation (nodes supporting different replication versions).
func (c *Cluster) AddNodeCaps(id raft.ServerID, dir string, durable bool, maxSchema, snapFormat int, features []string) (*Node, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	addr := raft.ServerAddress("node-" + string(id))
	nt := NewNodeTransport(id, addr, c.Fabric)

	var logStore raft.LogStore
	var stable raft.StableStore
	if durable {
		bs, err := NewBoltStore(filepath.Join(dir, "raft.db"))
		if err != nil {
			return nil, err
		}
		logStore, stable = bs, bs
		c.stores[id] = bs
	} else {
		in := raft.NewInmemStore()
		logStore, stable = in, in
	}
	snaps, err := raft.NewFileSnapshotStore(filepath.Join(dir, "snapshots"), 3, nil)
	if err != nil {
		return nil, err
	}

	bootstrap := len(c.Nodes) == 0
	node, err := NewNode(NodeOptions{
		ID:                          id,
		Address:                     addr,
		Transport:                   nt,
		LogStore:                    logStore,
		StableStore:                 stable,
		SnapshotStore:               snaps,
		Fabric:                      c.Fabric,
		Bootstrap:                   bootstrap,
		HeartbeatTimeout:            c.heartbeat,
		ElectionTimeout:             c.election,
		CommitTimeout:               c.commit,
		LeaderLeaseTimeout:          c.lease,
		ProposeTimeout:              c.propose,
		SupportedReplicationVersion: maxSchema,
		SupportedSnapshotFormat:     snapFormat,
		Features:                    features,
	})
	if err != nil {
		return nil, err
	}
	c.Fabric.RegisterNode(node)
	c.Nodes[id] = node
	return node, nil
}

// Join adds node id/addr to the cluster through the current leader (voter).
func (c *Cluster) Join(id raft.ServerID, addr raft.ServerAddress) error {
	leader := c.Leader()
	if leader == nil {
		return fmt.Errorf("replication: no leader to join through")
	}
	return leader.AddVoter(id, addr)
}

// AddLearner adds node id/addr as a non-voting replica through the leader.
func (c *Cluster) AddLearner(id raft.ServerID, addr raft.ServerAddress) error {
	leader := c.Leader()
	if leader == nil {
		return fmt.Errorf("replication: no leader to add learner through")
	}
	return leader.AddNonvoter(id, addr)
}

// Leader returns the current leader node, or nil if none is elected yet.
func (c *Cluster) Leader() *Node {
	for _, n := range c.Nodes {
		if n.State() == raft.Leader {
			return n
		}
	}
	return nil
}

// Propose routes an operation to node id: the node proposes when it is the
// leader, or forwards to the current leader through the fabric otherwise.
func (c *Cluster) Propose(ctx context.Context, id raft.ServerID, op Operation) (*ApplyResult, error) {
	n, ok := c.Nodes[id]
	if !ok {
		return nil, fmt.Errorf("replication: unknown node %q", id)
	}
	return n.handlePropose(ctx, op)
}

// WaitLeader blocks until some node is elected leader and returns it.
func (c *Cluster) WaitLeader(timeout time.Duration) (*Node, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n := c.Leader(); n != nil {
			return n, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, fmt.Errorf("replication: no leader elected within %s", timeout)
}

// BarrierAll runs a barrier on every node so committed entries are applied.
func (c *Cluster) BarrierAll() error {
	for _, n := range c.Nodes {
		if err := n.Barrier(); err != nil {
			return err
		}
	}
	return nil
}

// ShutdownAll shuts every node down cleanly.
func (c *Cluster) ShutdownAll() {
	for _, n := range c.Nodes {
		_ = n.Shutdown()
	}
}

// CloseNode shuts a node down and closes its durable store, releasing the
// bbolt file lock so the node can be reopened from the same directory.
func (c *Cluster) CloseNode(id raft.ServerID) error {
	if n, ok := c.Nodes[id]; ok {
		_ = n.Shutdown()
	}
	if bs, ok := c.stores[id]; ok {
		return bs.Close()
	}
	return nil
}

// CloseAll shuts every node down and closes all durable stores.
func (c *Cluster) CloseAll() {
	c.ShutdownAll()
	for id, bs := range c.stores {
		_ = bs.Close()
		delete(c.stores, id)
	}
}

// State returns a map of node id to raft state, for assertions.
func (c *Cluster) State() map[raft.ServerID]raft.RaftState {
	out := make(map[raft.ServerID]raft.RaftState, len(c.Nodes))
	for id, n := range c.Nodes {
		out[id] = n.State()
	}
	return out
}
