package replication

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// --- quorum / failure matrix ------------------------------------------------

func TestQuorumMatrix(t *testing.T) {
	cases := []struct {
		nodes       int
		isolate     int // number of voters to isolate
		wantWriteOK bool
	}{
		{nodes: 2, isolate: 0, wantWriteOK: true},
		{nodes: 2, isolate: 1, wantWriteOK: false},
		{nodes: 3, isolate: 1, wantWriteOK: true},
		{nodes: 4, isolate: 1, wantWriteOK: true},
		{nodes: 4, isolate: 2, wantWriteOK: false},
		{nodes: 5, isolate: 2, wantWriteOK: true},
		{nodes: 5, isolate: 3, wantWriteOK: false},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%dv_isolate%d", tc.nodes, tc.isolate)
		t.Run(name, func(t *testing.T) {
			c := newVoterCluster(t, tc.nodes)
			ids := make([]raft.ServerID, 0, tc.nodes)
			for i := 1; i <= tc.nodes; i++ {
				ids = append(ids, raft.ServerID(fmt.Sprintf("n%d", i)))
			}
			// baseline write while all healthy
			proposeSuccess(t, c, kvSet("op-base", "foo", "A", 0))

			for _, id := range ids[:tc.isolate] {
				c.Fabric.Isolate(id)
			}
			// Let leadership settle.
			time.Sleep(2 * c.election)

			op := kvSet("op-quorum", "quorum", "yes", 0)
			var lastErr error
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				l := c.Leader()
				if l == nil {
					lastErr = fmt.Errorf("no leader")
					time.Sleep(20 * time.Millisecond)
					continue
				}
				_, lastErr = l.Propose(context.Background(), op)
				if tc.wantWriteOK && lastErr == nil {
					break
				}
				if !tc.wantWriteOK && lastErr != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if tc.wantWriteOK {
				if lastErr != nil {
					t.Fatalf("expected write to succeed with %d/4 majority: %v", tc.nodes-tc.isolate, lastErr)
				}
				// reconnect isolated nodes; all converge
				for _, id := range ids[:tc.isolate] {
					c.Fabric.Reconnect(id)
				}
				waitFor(t, 20*time.Second, "all nodes converge after restoration", func() bool {
					v, _, ok := fsmGet(t, c.Nodes[ids[tc.isolate]], "quorum")
					return ok && v == "yes"
				})
			} else {
				if lastErr == nil {
					t.Fatal("expected write to be rejected without quorum, but it succeeded")
				}
			}
		})
	}
}

// --- restart / durable recovery --------------------------------------------

func TestRestartRecoverySingleNode(t *testing.T) {
	dir := t.TempDir()

	c1 := NewCluster()
	mustAddNode(t, c1, "n1", dir, true)
	n1, err := c1.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	op := kvSet("op-1", "foo", "A", 0)
	first, err := n1.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n1.Propose(context.Background(), kvSet("op-2", "bar", "B", 0)); err != nil {
		t.Fatal(err)
	}
	c1.CloseAll()

	// Reopen from the same durable directory.
	c2 := NewCluster()
	defer c2.CloseAll()
	n2 := mustAddNode(t, c2, "n1", dir, true)
	if _, err := c2.WaitLeader(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitValue(t, n2, "foo", "A")
	waitValue(t, n2, "bar", "B")

	// Durable idempotency survived restart: retry returns the original result.
	retry, err := n2.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Index != first.Index || retry.Revision != first.Revision {
		t.Fatalf("idempotency did not survive restart: %+v vs %+v", first, retry)
	}
	assertValue(t, n2, "foo", "A")
}

func TestFollowerRestartRejoins(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	for i := 0; i < 3; i++ {
		mustAddNode(t, c, raft.ServerID(fmt.Sprintf("n%d", i+1)), dirs[i], true)
	}
	joinVoterRetry(t, c, "n2")
	joinVoterRetry(t, c, "n3")
	waitLeaderKnown(t, c)

	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))
	waitValue(t, c.Nodes["n3"], "foo", "A")

	// Kill follower n3 (shutdown + close durable store).
	if err := c.CloseNode("n3"); err != nil {
		t.Fatal(err)
	}
	// Reopen n3 into the same cluster/fabric; the running leader catches it up.
	mustAddNode(t, c, "n3", dirs[2], true)
	waitValue(t, c.Nodes["n3"], "foo", "A")

	// Leader keeps writing; the rejoined follower converges.
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))
	waitValue(t, c.Nodes["n3"], "bar", "B")
}

func TestCompleteClusterRestart(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}

	c1 := NewCluster()
	for i := 0; i < 3; i++ {
		mustAddNode(t, c1, raft.ServerID(fmt.Sprintf("n%d", i+1)), dirs[i], true)
	}
	joinVoterRetry(t, c1, "n2")
	joinVoterRetry(t, c1, "n3")
	waitLeaderKnown(t, c1)
	proposeSuccess(t, c1, kvSet("op-1", "foo", "A", 0))
	waitValue(t, c1.Nodes["n3"], "foo", "A")
	c1.CloseAll()

	// Full restart: configuration is persisted in the replicated log, so no
	// re-joining is required.
	c2 := NewCluster()
	defer c2.CloseAll()
	for i := 0; i < 3; i++ {
		mustAddNode(t, c2, raft.ServerID(fmt.Sprintf("n%d", i+1)), dirs[i], true)
	}
	if _, err := c2.WaitLeader(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitValue(t, c2.Nodes["n1"], "foo", "A")
	waitValue(t, c2.Nodes["n2"], "foo", "A")
	waitValue(t, c2.Nodes["n3"], "foo", "A")

	proposeSuccess(t, c2, kvSet("op-2", "bar", "B", 0))
	waitValue(t, c2.Nodes["n1"], "bar", "B")
	waitValue(t, c2.Nodes["n2"], "bar", "B")
	waitValue(t, c2.Nodes["n3"], "bar", "B")
}

// --- membership -------------------------------------------------------------

func TestMembershipAndLeadershipTransfer(t *testing.T) {
	c := newVoterCluster(t, 2)
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))

	// Add a learner (non-voter) and let it catch up.
	mustAddNode(t, c, "n3", t.TempDir(), false)
	if err := c.AddLearner("n3", "node-n3"); err != nil {
		t.Fatal(err)
	}
	waitValue(t, c.Nodes["n3"], "foo", "A")
	cfg, err := c.Leader().Configuration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 3 || cfg[2].Suffrage != raft.Nonvoter {
		t.Fatalf("learner not non-voter: %+v", cfg)
	}

	// Promote the learner to voter.
	leader := c.Leader()
	if err := leader.AddVoter("n3", "node-n3"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "n3 promoted to voter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "n3" && s.Suffrage == raft.Voter {
				return true
			}
		}
		return false
	})

	// Remove voter n2 safely; n1+n3 retain quorum.
	leader = c.Leader()
	if err := leader.RemoveServer("n2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "n2 removed", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "n2" {
				return false
			}
		}
		return true
	})
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))

	// Leadership transfer: a different node becomes leader and writes still work.
	before := c.Leader().ID()
	_ = before
	leader = c.Leader()
	if err := leader.LeadershipTransfer(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "leadership transferred", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != before
	})
	proposeSuccess(t, c, kvSet("op-3", "baz", "C", 0))
}

// --- snapshot ---------------------------------------------------------------

func TestSnapshotRestartAndIdempotency(t *testing.T) {
	dir := t.TempDir()
	c1 := NewCluster()
	mustAddNode(t, c1, "n1", dir, true)
	n1, err := c1.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	op := kvSet("op-1", "foo", "A", 0)
	first, err := n1.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n1.Propose(context.Background(), kvSet("op-2", "bar", "B", 0)); err != nil {
		t.Fatal(err)
	}
	if err := n1.Snapshot(); err != nil {
		t.Fatal(err)
	}
	c1.CloseAll()

	c2 := NewCluster()
	defer c2.CloseAll()
	n2 := mustAddNode(t, c2, "n1", dir, true)
	if _, err := c2.WaitLeader(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitValue(t, n2, "foo", "A")
	waitValue(t, n2, "bar", "B")

	retry, err := n2.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Index != first.Index || retry.Revision != first.Revision {
		t.Fatalf("idempotency did not survive snapshot/restart: %+v vs %+v", first, retry)
	}
	assertValue(t, n2, "foo", "A")
}
