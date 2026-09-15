package replication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// Fabric is a deterministic, fault-injectable in-process network for the
// replicated-state harness. It routes both the consensus protocol messages
// (raft.Transport) and the application-level proposal forwarding between
// nodes, and it can drop or block messages in either direction so partitions,
// isolation and stale-leader scenarios are reproducible without real network
// timing.
type Fabric struct {
	mu          sync.Mutex
	byID        map[raft.ServerID]*nodeTransport
	byAddr      map[raft.ServerAddress]*nodeTransport
	nodes       map[raft.ServerID]*Node
	caps        map[raft.ServerID]Capabilities
	partitioned map[fabricEdge]bool
	sendTimeout time.Duration
}

type fabricEdge struct{ from, to raft.ServerID }

// NewFabric returns an empty fault-injectable fabric.
func NewFabric() *Fabric {
	return &Fabric{
		byID:        make(map[raft.ServerID]*nodeTransport),
		byAddr:      make(map[raft.ServerAddress]*nodeTransport),
		nodes:       make(map[raft.ServerID]*Node),
		caps:        make(map[raft.ServerID]Capabilities),
		partitioned: make(map[fabricEdge]bool),
		sendTimeout: 500 * time.Millisecond,
	}
}

// registerTransport wires a node transport into the fabric.
func (f *Fabric) registerTransport(nt *nodeTransport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[nt.id] = nt
	f.byAddr[nt.addr] = nt
}

// RegisterNode wires a Node so proposal forwarding can route to it.
func (f *Fabric) RegisterNode(n *Node) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[n.id] = n
}

// RegisterCapabilities records a node's advertised replication capabilities
// (schema negotiation input). Compatibility is never inferred from version
// strings.
func (f *Fabric) RegisterCapabilities(id raft.ServerID, c Capabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caps[id] = c
}

// CapabilitiesOf returns a node's advertised capabilities.
func (f *Fabric) CapabilitiesOf(id raft.ServerID) (Capabilities, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.caps[id]
	return c, ok
}

// Partition drops all messages from -> to (one direction only).
func (f *Fabric) Partition(from, to raft.ServerID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partitioned[fabricEdge{from, to}] = true
}

// Unpartition restores the from -> to direction.
func (f *Fabric) Unpartition(from, to raft.ServerID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.partitioned, fabricEdge{from, to})
}

// Isolate blocks all messages involving id (both directions).
func (f *Fabric) Isolate(id raft.ServerID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for to := range f.byID {
		f.partitioned[fabricEdge{id, to}] = true
		f.partitioned[fabricEdge{to, id}] = true
	}
}

// Reconnect removes all partition rules involving id.
func (f *Fabric) Reconnect(id raft.ServerID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for e := range f.partitioned {
		if e.from == id || e.to == id {
			delete(f.partitioned, e)
		}
	}
}

func (f *Fabric) blocked(from, to raft.ServerID) bool {
	return f.partitioned[fabricEdge{from, to}]
}

// route delivers a raft protocol RPC from -> to. When blocked it returns an
// error (and drains any snapshot reader so the sender can close it).
func (f *Fabric) route(from raft.ServerID, target raft.ServerAddress, args interface{}, r io.Reader, timeout time.Duration) (raft.RPCResponse, error) {
	f.mu.Lock()
	nt, ok := f.byAddr[target]
	blocked := f.blocked(from, nt.id)
	f.mu.Unlock()
	if !ok {
		return raft.RPCResponse{}, fmt.Errorf("replication: unknown peer address %q", target)
	}
	if blocked {
		if r != nil {
			_, _ = io.Copy(io.Discard, r)
		}
		return raft.RPCResponse{}, fmt.Errorf("replication: partitioned %s -> %s", from, nt.id)
	}

	respCh := make(chan raft.RPCResponse, 1)
	req := raft.RPC{Command: args, Reader: r, RespChan: respCh}
	select {
	case nt.consumerCh <- req:
	case <-time.After(timeout):
		return raft.RPCResponse{}, errors.New("replication: send timed out")
	}
	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(timeout):
		return raft.RPCResponse{}, errors.New("replication: command timed out")
	}
}

// ForwardProposal routes an application-level proposal from a follower to the
// leader node, respecting the same partition policy. A partitioned follower
// cannot reach the leader, forcing the client retry path.
func (f *Fabric) ForwardProposal(from, to raft.ServerID, op Operation, timeout time.Duration) (*ApplyResult, error) {
	f.mu.Lock()
	node, ok := f.nodes[to]
	blocked := f.blocked(from, to)
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("replication: unknown node %q", to)
	}
	if blocked {
		return nil, fmt.Errorf("replication: proposal forwarding partitioned %s -> %s", from, to)
	}
	return node.proposeLocal(context.Background(), op)
}

// nodeTransport implements raft.Transport over the Fabric, mirroring the
// in-memory transport but with injectable partitions.
type nodeTransport struct {
	id         raft.ServerID
	addr       raft.ServerAddress
	fabric     *Fabric
	consumerCh chan raft.RPC
}

// NewNodeTransport creates a raft transport for node id/addr over the fabric.
func NewNodeTransport(id raft.ServerID, addr raft.ServerAddress, f *Fabric) *nodeTransport {
	nt := &nodeTransport{
		id:         id,
		addr:       addr,
		fabric:     f,
		consumerCh: make(chan raft.RPC, 64),
	}
	if f != nil {
		f.registerTransport(nt)
	}
	return nt
}

var _ raft.Transport = (*nodeTransport)(nil)

// Consumer implements raft.Transport.
func (nt *nodeTransport) Consumer() <-chan raft.RPC { return nt.consumerCh }

// LocalAddr implements raft.Transport.
func (nt *nodeTransport) LocalAddr() raft.ServerAddress { return nt.addr }

// AppendEntriesPipeline is not supported; raft falls back to AppendEntries.
func (nt *nodeTransport) AppendEntriesPipeline(id raft.ServerID, target raft.ServerAddress) (raft.AppendPipeline, error) {
	return nil, raft.ErrPipelineReplicationNotSupported
}

// AppendEntries implements raft.Transport.
func (nt *nodeTransport) AppendEntries(id raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
	rpcResp, err := nt.fabric.route(nt.id, target, args, nil, nt.fabric.sendTimeout)
	if err != nil {
		return err
	}
	out, ok := rpcResp.Response.(*raft.AppendEntriesResponse)
	if !ok {
		return fmt.Errorf("replication: unexpected response type %T", rpcResp.Response)
	}
	*resp = *out
	return nil
}

// RequestVote implements raft.Transport.
func (nt *nodeTransport) RequestVote(id raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest, resp *raft.RequestVoteResponse) error {
	rpcResp, err := nt.fabric.route(nt.id, target, args, nil, nt.fabric.sendTimeout)
	if err != nil {
		return err
	}
	out, ok := rpcResp.Response.(*raft.RequestVoteResponse)
	if !ok {
		return fmt.Errorf("replication: unexpected response type %T", rpcResp.Response)
	}
	*resp = *out
	return nil
}

// InstallSnapshot implements raft.Transport.
func (nt *nodeTransport) InstallSnapshot(id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest, resp *raft.InstallSnapshotResponse, data io.Reader) error {
	rpcResp, err := nt.fabric.route(nt.id, target, args, data, 10*nt.fabric.sendTimeout)
	if err != nil {
		return err
	}
	out, ok := rpcResp.Response.(*raft.InstallSnapshotResponse)
	if !ok {
		return fmt.Errorf("replication: unexpected response type %T", rpcResp.Response)
	}
	*resp = *out
	return nil
}

// EncodePeer implements raft.Transport.
func (nt *nodeTransport) EncodePeer(id raft.ServerID, addr raft.ServerAddress) []byte {
	return []byte(addr)
}

// DecodePeer implements raft.Transport.
func (nt *nodeTransport) DecodePeer(b []byte) raft.ServerAddress { return raft.ServerAddress(b) }

// SetHeartbeatHandler is a no-op; heartbeats flow through the consumer.
func (nt *nodeTransport) SetHeartbeatHandler(cb func(rpc raft.RPC)) {}

// TimeoutNow implements raft.Transport.
func (nt *nodeTransport) TimeoutNow(id raft.ServerID, target raft.ServerAddress, args *raft.TimeoutNowRequest, resp *raft.TimeoutNowResponse) error {
	rpcResp, err := nt.fabric.route(nt.id, target, args, nil, 10*nt.fabric.sendTimeout)
	if err != nil {
		return err
	}
	out, ok := rpcResp.Response.(*raft.TimeoutNowResponse)
	if !ok {
		return fmt.Errorf("replication: unexpected response type %T", rpcResp.Response)
	}
	*resp = *out
	return nil
}

// Close is a no-op; the fabric owns the transports.
func (nt *nodeTransport) Close() error { return nil }
