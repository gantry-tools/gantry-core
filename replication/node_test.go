package replication

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// CP7B-4/5: acknowledgement, durable idempotency, follower forwarding and
// leader/stale-leader fencing.

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

func mustAddNode(t *testing.T, c *Cluster, id raft.ServerID, dir string, durable bool) *Node {
	t.Helper()
	n, err := c.AddNode(id, dir, durable)
	if err != nil {
		t.Fatalf("add node %s: %v", id, err)
	}
	return n
}

func joinVoterRetry(t *testing.T, c *Cluster, id raft.ServerID) {
	t.Helper()
	addr := raft.ServerAddress("node-" + string(id))
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		l := c.Leader()
		if l == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err := l.AddVoter(id, addr); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("join voter %s timed out", id)
}

func waitLeaderKnown(t *testing.T, c *Cluster) *Node {
	t.Helper()
	var leader *Node
	waitFor(t, 20*time.Second, "all nodes agree on a common leader", func() bool {
		var known raft.ServerID
		for _, n := range c.Nodes {
			_, lid := n.Leader()
			if lid == "" {
				return false
			}
			if known == "" {
				known = lid
			} else if lid != known {
				return false
			}
			if n.State() == raft.Leader {
				leader = n
			}
		}
		return leader != nil && leader.ID() == known
	})
	return leader
}

func newVoterCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	for i := 0; i < n; i++ {
		mustAddNode(t, c, raft.ServerID(fmt.Sprintf("n%d", i+1)), t.TempDir(), false)
	}
	for i := 1; i < n; i++ {
		joinVoterRetry(t, c, raft.ServerID(fmt.Sprintf("n%d", i+1)))
	}
	waitLeaderKnown(t, c)
	return c
}

func proposeSuccess(t *testing.T, c *Cluster, op Operation) *ApplyResult {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		l := c.Leader()
		if l == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		res, err := l.Propose(context.Background(), op)
		if err == nil {
			return res
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("propose %s timed out", op.ID)
	return nil
}

func fsmGet(t *testing.T, n *Node, key string) (string, int64, bool) {
	t.Helper()
	v, r, ok := n.FSM().Get(key)
	return v, r, ok
}

func assertValue(t *testing.T, n *Node, key, want string) {
	t.Helper()
	v, _, ok := fsmGet(t, n, key)
	if !ok || v != want {
		t.Fatalf("node %s: key %q = %q (ok=%v), want %q", n.ID(), key, v, ok, want)
	}
}

func waitValue(t *testing.T, n *Node, key, want string) {
	t.Helper()
	waitFor(t, 20*time.Second, fmt.Sprintf("node %s key %q == %q", n.ID(), key, want), func() bool {
		v, _, ok := fsmGet(t, n, key)
		return ok && v == want
	})
}

func TestStandaloneProposeAndAck(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "n1", t.TempDir(), false)
	n, err := c.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	res, err := n.Propose(context.Background(), kvSet("op-1", "foo", "A", 0))
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if res.Index == 0 || res.Revision != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	assertValue(t, n, "foo", "A")

	// acknowledgement contract: committed AND applied on the leader.
	idx, _ := n.AppliedIndex()
	if res.Index > idx {
		t.Fatalf("acknowledged index %d beyond applied index %d", res.Index, idx)
	}
}

func TestIdempotentRetryAfterLostResponse(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "n1", t.TempDir(), false)
	n, err := c.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	op := kvSet("op-1", "foo", "A", 0)
	first, err := n.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the client seeing a lost response and retrying the same ID.
	second, err := n.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if second.OpID != first.OpID || second.Index != first.Index || second.Revision != first.Revision {
		t.Fatalf("retry did not resolve to the original committed result: %+v vs %+v", first, second)
	}
	// No second mutation: revision unchanged.
	v, rev, _ := fsmGet(t, n, "foo")
	if v != "A" || rev != 1 {
		t.Fatalf("idempotent retry mutated state: %q rev=%d", v, rev)
	}

	// Same ID with a different payload must fail closed before proposing.
	other := kvSet("op-1", "foo", "B", 0)
	if _, err := n.Propose(context.Background(), other); err == nil || !strings.Contains(err.Error(), "different payload") {
		t.Fatalf("same-ID/different-payload must fail closed, got %v", err)
	}
	assertValue(t, n, "foo", "A")
}

func TestFollowerForwarding(t *testing.T) {
	c := newVoterCluster(t, 3)

	// Propose through a follower: it must forward to the leader.
	res, err := c.Propose(context.Background(), "n2", kvSet("op-1", "foo", "A", 0))
	if err != nil {
		t.Fatalf("forwarded propose: %v", err)
	}
	if res.OpID != "op-1" || res.Revision != 1 {
		t.Fatalf("unexpected forwarded result: %+v", res)
	}
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c.Nodes[id], "foo", "A")
	}

	// Partition the follower's forwarding path to the leader: forwarding fails.
	_, leaderID := c.Leader().Leader()
	c.Fabric.Partition("n2", leaderID)
	if _, err := c.Propose(context.Background(), "n2", kvSet("op-2", "bar", "X", 0)); err == nil {
		t.Fatal("forwarding across a partition must fail")
	}
	c.Fabric.Unpartition("n2", leaderID)
}

func TestStaleLeaderRejected(t *testing.T) {
	c := newVoterCluster(t, 3)
	old := c.Leader()
	oldID := old.ID()

	// Isolate the leader; the other two elect a new leader.
	c.Fabric.Isolate(oldID)
	waitFor(t, 20*time.Second, "a new leader is elected after isolating the old one", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != oldID
	})
	newLeader := c.Leader()
	if newLeader.ID() == oldID {
		t.Fatal("expected a new leader after isolating the old one")
	}

	// A write from the new majority commits.
	proposeSuccess(t, c, kvSet("op-1", "bar", "B", 0))

	// The stale/isolated former leader must never acknowledge a write: no quorum.
	if _, err := old.Propose(context.Background(), kvSet("op-2", "stale", "X", 0)); err == nil {
		t.Fatal("stale leader acknowledged a write without quorum")
	}

	// Reconnect: the old leader steps down and catches up.
	c.Fabric.Reconnect(oldID)
	waitFor(t, 20*time.Second, "stale leader becomes follower and catches up", func() bool {
		if old.State() == raft.Leader {
			return false
		}
		v, _, ok := fsmGet(t, old, "bar")
		return ok && v == "B"
	})
}

func TestUnsupportedVersionFailsClosed(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "n1", t.TempDir(), false)
	n, err := c.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	op := kvSet("op-1", "foo", "A", 0)
	op.Version = Version + 1
	if _, err := n.Propose(context.Background(), op); err == nil || !strings.Contains(err.Error(), "unsupported replication operation version") {
		t.Fatalf("unsupported version must fail closed, got %v", err)
	}
	if _, _, ok := fsmGet(t, n, "foo"); ok {
		t.Fatal("rejected operation must not mutate state")
	}
}
