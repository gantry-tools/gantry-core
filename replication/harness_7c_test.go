package replication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// --- helpers ---------------------------------------------------------------

func waitLeader(t *testing.T, c *Cluster) *Node {
	t.Helper()
	n, err := c.WaitLeader(15 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func setSchema(v int, id, key, value string, rev int64) Operation {
	o := kvSet(id, key, value, rev)
	o.Version = v
	return o
}

func persistSnapshot(t *testing.T, snap raft.FSMSnapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := snap.Persist(snapshotSink{&buf}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeN(t *testing.T, c *Cluster, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		proposeSuccess(t, c, kvSet(fmt.Sprintf("%s-op-%d", prefix, i), fmt.Sprintf("%s/key-%d", prefix, i), "V", 0))
	}
}

// proposeMustFail performs a single proposal attempt (no retry) and returns
// the error, used for assertions that a write must be rejected.
func proposeMustFail(t *testing.T, c *Cluster, op Operation) error {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		l := c.Leader()
		if l == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		_, err := l.Propose(context.Background(), op)
		return err
	}
	return fmt.Errorf("no leader available")
}

// --- CP7C-1: canonical snapshot format --------------------------------------

func TestSnapshotCanonicalForm(t *testing.T) {
	ops := []Operation{setSchema(2, "op-1", "foo", "A", 0), setSchema(2, "op-2", "bar", "B", 0)}
	a, b := NewKVFSM(), NewKVFSM()
	applyLog(t, a, ops)
	applyLog(t, b, ops)

	sa, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ba := persistSnapshot(t, sa)
	bb := persistSnapshot(t, sb)
	if !bytes.Equal(ba, bb) {
		t.Fatal("equal semantic state produced different canonical snapshots")
	}

	// restore(snapshot) -> identical semantic state; snapshot->restore->snapshot
	// is canonically equivalent.
	d := NewKVFSM()
	if err := d.Restore(snapshotReader{bytes.NewReader(ba)}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"foo", "bar"} {
		va, ra, oka := a.Get(key)
		vd, rd, okd := d.Get(key)
		if oka != okd || va != vd || ra != rd {
			t.Fatalf("restore diverged on %q: (%v,%q,%d) vs (%v,%q,%d)", key, oka, va, ra, okd, vd, rd)
		}
	}
	sd, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	bd := persistSnapshot(t, sd)
	if !bytes.Equal(ba, bd) {
		t.Fatal("snapshot -> restore -> snapshot is not canonically equivalent")
	}

	// The envelope carries the expected semantic fields, never product/node storage.
	var env SnapshotEnvelope
	if err := json.Unmarshal(ba, &env); err != nil {
		t.Fatal(err)
	}
	if env.FormatVersion != SnapshotFormatVersion || env.ReplicationVersion == 0 {
		t.Fatalf("snapshot envelope missing semantic fields: %+v", env)
	}
}

// --- CP7C-2: node-local exclusion -------------------------------------------

func TestNodeLocalExclusion(t *testing.T) {
	c := newVoterCluster(t, 3)
	// Populate node-local identity/credential-like state unique to each node.
	for i, id := range []raft.ServerID{"n1", "n2", "n3"} {
		c.Nodes[id].SetLocal("node.identity", string(id))
		c.Nodes[id].SetLocal("node.credential", fmt.Sprintf("secret-%s", id))
		c.Nodes[id].SetLocal("node.nonce", fmt.Sprintf("nonce-%d", i))
	}

	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))

	// Export A's filtered logical snapshot; decode it.
	aSnap, err := c.Nodes["n1"].ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeSnapshot(aSnap)
	if err != nil {
		t.Fatal(err)
	}
	// Structural exclusion: node-local keys never appear in replicated state.
	for _, k := range []string{"node.identity", "node.credential", "node.nonce"} {
		if _, ok := env.State[k]; ok {
			t.Fatalf("node-local key %q leaked into the snapshot", k)
		}
	}

	// Install onto a fresh standalone node D; D's own local state is preserved
	// and A's node-local state is absent.
	dir := t.TempDir()
	c2 := NewCluster()
	defer c2.CloseAll()
	d := mustAddNode(t, c2, "d", dir, false)
	d.SetLocal("node.identity", "d")
	d.SetLocal("node.credential", "secret-d")
	if err := d.ApplySnapshot(aSnap); err != nil {
		t.Fatal(err)
	}
	if v, _, ok := fsmGet(t, d, "foo"); !ok || v != "A" {
		t.Fatalf("replicated state not transferred to D: %q", v)
	}
	if v, ok := d.GetLocal("node.identity"); !ok || v != "d" {
		t.Fatalf("D's own local identity not preserved: %q ok=%v", v, ok)
	}
	if v, ok := d.GetLocal("node.credential"); !ok || v != "secret-d" {
		t.Fatalf("D's own local credential not preserved: %q ok=%v", v, ok)
	}
	if v, ok := d.GetLocal("node.credential"); ok && v == "secret-n1" {
		t.Fatal("A's node-local credential leaked into D")
	}
}

// --- CP7C-3/4: snapshot catch-up and compaction ------------------------------

func TestSnapshotCatchUpAndPreSnapshotIdempotency(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	for i := 0; i < 3; i++ {
		mustAddNode(t, c, raft.ServerID(fmt.Sprintf("n%d", i+1)), t.TempDir(), false)
	}
	joinVoterRetry(t, c, "n2")
	joinVoterRetry(t, c, "n3")
	waitLeaderKnown(t, c)

	// Add a learner D and isolate it before it can catch up.
	mustAddNode(t, c, "d", t.TempDir(), false)
	if err := c.AddLearner("d", "node-d"); err != nil {
		t.Fatal(err)
	}
	c.Fabric.Isolate("d")

	// Committed pre-snapshot operation whose log entry will be compacted.
	preOp := kvSet("pre-op", "pre", "P", 0)
	first, err := c.Leader().Propose(context.Background(), preOp)
	if err != nil {
		t.Fatal(err)
	}
	// Advance well beyond it, then compact through the log.
	writeN(t, c, "adv", 30)
	if err := c.Leader().Snapshot(); err != nil {
		t.Fatal(err)
	}
	_ = first

	// Reconnect D: it must install the snapshot and replay the trailing log.
	c.Fabric.Reconnect("d")
	waitFor(t, 30*time.Second, "isolated learner D catches up via snapshot + trailing log", func() bool {
		l := c.Leader()
		_, la := l.AppliedIndex()
		_, da := c.Nodes["d"].AppliedIndex()
		return da >= la
	})
	waitValue(t, c.Nodes["d"], "adv/key-29", "V")
	waitValue(t, c.Nodes["d"], "pre", "P")

	// Pre-snapshot operation-ID idempotency survives compaction: retrying it
	// resolves to the original committed result, not a new mutation.
	retry, err := c.Leader().Propose(context.Background(), preOp)
	if err != nil {
		t.Fatal(err)
	}
	if retry.OpID != first.OpID || retry.Index != first.Index || retry.Revision != first.Revision {
		t.Fatalf("pre-snapshot retry did not resolve to the original result: %+v vs %+v", first, retry)
	}
	v, rev, _ := fsmGet(t, c.Nodes["d"], "pre")
	if v != "P" || rev != 1 {
		t.Fatalf("pre-snapshot retry mutated state: %q rev=%d", v, rev)
	}
}

func TestCompactionRepeatedSnapshotRestartContinue(t *testing.T) {
	dirs := t.TempDir()
	c := NewCluster()
	defer c.CloseAll()
	mustAddNode(t, c, "n1", dirs, true)
	waitLeader(t, c)
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))
	if err := c.Nodes["n1"].Snapshot(); err != nil {
		t.Fatal(err)
	}
	c.CloseAll()

	for i := 0; i < 2; i++ {
		c2 := NewCluster()
		n2 := mustAddNode(t, c2, "n1", dirs, true)
		waitLeader(t, c2)
		waitValue(t, n2, "foo", "A")
		key := fmt.Sprintf("cycle-%d", i)
		proposeSuccess(t, c2, kvSet(fmt.Sprintf("op-%d", i+2), key, "V", 0))
		if err := n2.Snapshot(); err != nil {
			t.Fatal(err)
		}
		c2.CloseAll()
	}
	c3 := NewCluster()
	defer c3.CloseAll()
	n3 := mustAddNode(t, c3, "n1", dirs, true)
	waitLeader(t, c3)
	waitValue(t, n3, "foo", "A")
	waitValue(t, n3, "cycle-0", "V")
	waitValue(t, n3, "cycle-1", "V")
	proposeSuccess(t, c3, kvSet("op-final", "final", "Z", 0))
	waitValue(t, n3, "final", "Z")
}

// --- CP7C-5: learner bootstrap -----------------------------------------------

func TestLearnerCannotAffectQuorumAndPromoteAfterCatchUp(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "n1", t.TempDir(), false)
	mustAddNode(t, c, "n2", t.TempDir(), false)
	joinVoterRetry(t, c, "n2")
	waitLeaderKnown(t, c)

	// Add a learner and isolate it so it lags.
	mustAddNode(t, c, "l", t.TempDir(), false)
	if err := c.AddLearner("l", "node-l"); err != nil {
		t.Fatal(err)
	}
	c.Fabric.Isolate("l")

	// A lagging learner must not affect quorum: writes still commit on n1+n2.
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))
	waitValue(t, c.Nodes["n1"], "foo", "A")
	waitValue(t, c.Nodes["n2"], "foo", "A")
	if _, _, ok := fsmGet(t, c.Nodes["l"], "foo"); ok {
		t.Fatal("isolated learner should not have the committed value")
	}

	// Reconnect and let the learner catch up before promotion.
	c.Fabric.Reconnect("l")
	waitFor(t, 30*time.Second, "learner catches up", func() bool {
		l := c.Leader()
		_, la := l.AppliedIndex()
		_, da := c.Nodes["l"].AppliedIndex()
		return da >= la
	})
	waitValue(t, c.Nodes["l"], "foo", "A")

	// Promote only after catch-up; it then becomes a voter.
	if err := c.Leader().AddVoter("l", "node-l"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "learner promoted to voter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "l" && s.Suffrage == raft.Voter {
				return true
			}
		}
		return false
	})
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))
	waitValue(t, c.Nodes["l"], "bar", "B")
}

// --- CP7C-6: membership lifecycle --------------------------------------------

func TestMembershipLifecycle(t *testing.T) {
	c := newVoterCluster(t, 3)

	// Remove a failed voter: isolate it, then remove it through the leader.
	c.Fabric.Isolate("n3")
	if err := c.Leader().RemoveServer("n3"); err != nil {
		t.Fatalf("remove failed voter: %v", err)
	}
	waitFor(t, 20*time.Second, "n3 removed from configuration", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "n3" {
				return false
			}
		}
		return true
	})

	// Replacement node restores quorum (n1+n2 remain; add n4 as voter).
	mustAddNode(t, c, "n4", t.TempDir(), false)
	if err := c.Leader().AddVoter("n4", "node-n4"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "n4 added as voter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "n4" && s.Suffrage == raft.Voter {
				return true
			}
		}
		return false
	})
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))

	// Leadership transfer before removing the current leader.
	before := c.Leader().ID()
	if err := c.Leader().LeadershipTransfer(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "leadership transferred", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != before
	})
	if err := c.Leader().RemoveServer(before); err != nil {
		t.Fatalf("remove former leader after transfer: %v", err)
	}
	waitFor(t, 20*time.Second, "former leader removed", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == before {
				return false
			}
		}
		return true
	})
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))
}

func TestConcurrentMembershipSerialized(t *testing.T) {
	c := newVoterCluster(t, 2)
	mustAddNode(t, c, "x", t.TempDir(), false)
	mustAddNode(t, c, "y", t.TempDir(), false)

	// Two concurrent membership requests: raft serializes configuration
	// changes; at most one is in flight at once. Both must eventually appear
	// (or the loser returns a configuration-in-progress error).
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []raft.ServerID{"x", "y"} {
		wg.Add(1)
		go func(i int, id raft.ServerID) {
			defer wg.Done()
			l := c.Leader()
			for l == nil {
				time.Sleep(10 * time.Millisecond)
				l = c.Leader()
			}
			errs[i] = l.AddNonvoter(id, raft.ServerAddress("node-"+string(id)))
		}(i, id)
	}
	wg.Wait()

	// Configuration changes are serialized by raft: the number of learners that
	// actually appear in the configuration must equal the number of successful
	// membership calls - never more, and never two silent concurrent changes.
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	waitFor(t, 20*time.Second, "membership changes applied", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		present := 0
		for _, s := range cfg {
			if s.ID == "x" || s.ID == "y" {
				present++
			}
		}
		return present == successes
	})
	if successes == 0 {
		t.Fatal("at least one membership call should have succeeded")
	}
}

// --- CP7C-7/8: replication-schema negotiation / rolling version --------------

func TestRollingSchemaActivation(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	// A supports only schema 1 (old); B and C support schema 1..2.
	mustAddNode(t, c, "a", t.TempDir(), false)
	c.Nodes["a"].SetSupportedReplicationVersion(1)
	mustAddNode(t, c, "b", t.TempDir(), false)
	mustAddNode(t, c, "c", t.TempDir(), false)
	joinVoterRetry(t, c, "b")
	joinVoterRetry(t, c, "c")
	waitLeaderKnown(t, c)

	// Default ops are schema 2. While voter a is old (max 1), schema-2 ops must
	// NOT be commit-eligible even though the leader understands them.
	if err := proposeMustFail(t, c, kvSet("op-2", "s2", "X", 0)); err == nil {
		t.Fatal("schema-2 operation must be blocked while an old voter is present")
	}
	if _, _, ok := fsmGet(t, c.Leader(), "s2"); ok {
		t.Fatal("blocked schema-2 operation must not be committed")
	}

	// Schema-1 operations remain allowed.
	proposeSuccess(t, c, setSchema(1, "op-1", "s1", "Y", 0))
	waitValue(t, c.Nodes["a"], "s1", "Y")

	// Upgrade the old voter; schema-2 now activates.
	c.Nodes["a"].SetSupportedReplicationVersion(Version)
	proposeSuccess(t, c, kvSet("op-3", "s2", "X", 0))
	for _, id := range []raft.ServerID{"a", "b", "c"} {
		waitValue(t, c.Nodes[id], "s2", "X")
	}
}

func TestLearnerDoesNotGateSchemaActivation(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "a", t.TempDir(), false)
	mustAddNode(t, c, "b", t.TempDir(), false)
	joinVoterRetry(t, c, "b")
	waitLeaderKnown(t, c)

	// A learner that only understands schema 1 is NOT part of the quorum
	// contract, so it must not gate schema-2 activation.
	mustAddNode(t, c, "l", t.TempDir(), false)
	c.Nodes["l"].SetSupportedReplicationVersion(1)
	if err := c.AddLearner("l", "node-l"); err != nil {
		t.Fatal(err)
	}
	proposeSuccess(t, c, kvSet("op-2", "s2", "X", 0))
	waitValue(t, c.Nodes["a"], "s2", "X")
	waitValue(t, c.Nodes["b"], "s2", "X")
}

// --- CP7C-10: adversarial / crash / restart around snapshots -----------------

func TestPostSnapshotRetryIdempotency(t *testing.T) {
	dir := t.TempDir()
	c1 := NewCluster()
	mustAddNode(t, c1, "n1", dir, true)
	n1, err := c1.WaitLeader(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	op := kvSet("post-op", "post", "P", 0)
	first, err := n1.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if err := n1.Snapshot(); err != nil {
		t.Fatal(err)
	}
	c1.CloseAll()

	c2 := NewCluster()
	defer c2.CloseAll()
	n2 := mustAddNode(t, c2, "n1", dir, true)
	waitLeader(t, c2)
	waitValue(t, n2, "post", "P")
	// Retry an operation committed after the snapshot boundary: idempotent.
	retry, err := n2.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Index != first.Index || retry.Revision != first.Revision {
		t.Fatalf("post-snapshot retry not idempotent: %+v vs %+v", first, retry)
	}
	waitValue(t, n2, "post", "P")
}

// A learner catching up must converge even if the leader changes mid-catch-up:
// the new leader has the same committed state and continues the snapshot/trailing
// log transfer.
func TestLearnerCatchUpAcrossLeaderChange(t *testing.T) {
	c := newVoterCluster(t, 3)
	mustAddNode(t, c, "d", t.TempDir(), false)
	if err := c.AddLearner("d", "node-d"); err != nil {
		t.Fatal(err)
	}
	c.Fabric.Isolate("d")
	writeN(t, c, "adv", 20)
	if err := c.Leader().Snapshot(); err != nil {
		t.Fatal(err)
	}

	// Reconnect the learner and immediately change the leader.
	c.Fabric.Reconnect("d")
	old := c.Leader()
	oldID := old.ID()
	c.Fabric.Isolate(oldID)
	waitFor(t, 20*time.Second, "a new leader is elected during learner catch-up", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != oldID
	})
	c.Fabric.Reconnect(oldID)

	// The learner converges to the (same) committed state under the new leader.
	waitFor(t, 30*time.Second, "learner catches up across a leader change", func() bool {
		l := c.Leader()
		if l == nil {
			return false
		}
		_, la := l.AppliedIndex()
		_, da := c.Nodes["d"].AppliedIndex()
		return da >= la
	})
	waitValue(t, c.Nodes["d"], "adv/key-19", "V")
}

func TestLearnerPromotionRefusedUntilCatchUp(t *testing.T) {
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	mustAddNode(t, c, "n1", t.TempDir(), false)
	mustAddNode(t, c, "n2", t.TempDir(), false)
	joinVoterRetry(t, c, "n2")
	waitLeaderKnown(t, c)
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))

	// Add a learner and isolate it BEFORE advancing the log, so it lags.
	mustAddNode(t, c, "l", t.TempDir(), false)
	if err := c.AddLearner("l", "node-l"); err != nil {
		t.Fatal(err)
	}
	c.Fabric.Isolate("l")
	// Writes commit on the voters while the learner is behind.
	writeN(t, c, "lag", 5)

	// Confirm the leader applied writes the isolated learner cannot see.
	waitFor(t, 10*time.Second, "leader advanced past the isolated learner", func() bool {
		l := c.Leader()
		if l == nil {
			return false
		}
		_, _, okLeader := l.FSM().(*KVFSM).Get("lag/key-4")
		_, _, okLearner := c.Nodes["l"].FSM().(*KVFSM).Get("lag/key-4")
		return okLeader && !okLearner
	})

	// Promotion must be refused while the learner is behind.
	if err := c.PromoteLearnerAfterCatchUp("l", 1500*time.Millisecond); err == nil {
		t.Fatal("promotion of a lagging learner must be refused")
	}
	// It remains a non-voter (cannot affect quorum).
	cfg, err := c.Leader().Configuration()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cfg {
		if s.ID == "l" {
			if s.Suffrage != raft.Nonvoter {
				t.Fatalf("lagging learner must remain a non-voter, got %v", s.Suffrage)
			}
		}
	}
	// A write still commits (learner never gates quorum).
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))

	// Recovery: reconnect, let it catch up, then promotion succeeds.
	c.Fabric.Reconnect("l")
	if err := c.PromoteLearnerAfterCatchUp("l", 20*time.Second); err != nil {
		t.Fatalf("promotion after catch-up: %v", err)
	}
	waitFor(t, 20*time.Second, "learner promoted to voter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == "l" && s.Suffrage == raft.Voter {
				return true
			}
		}
		return false
	})
}

func TestCompleteClusterRestartAfterCompaction(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	c1 := NewCluster()
	for i := 0; i < 3; i++ {
		mustAddNode(t, c1, raft.ServerID(fmt.Sprintf("n%d", i+1)), dirs[i], true)
	}
	joinVoterRetry(t, c1, "n2")
	joinVoterRetry(t, c1, "n3")
	waitLeaderKnown(t, c1)
	writeN(t, c1, "a", 25)
	if err := c1.Leader().Snapshot(); err != nil {
		t.Fatal(err)
	}
	proposeSuccess(t, c1, kvSet("mark", "mark", "M", 0))
	c1.CloseAll()

	c2 := NewCluster()
	defer c2.CloseAll()
	for i := 0; i < 3; i++ {
		mustAddNode(t, c2, raft.ServerID(fmt.Sprintf("n%d", i+1)), dirs[i], true)
	}
	if _, err := c2.WaitLeader(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c2.Nodes[id], "a/key-24", "V")
		waitValue(t, c2.Nodes[id], "mark", "M")
	}
	proposeSuccess(t, c2, kvSet("final", "final", "Z", 0))
	waitValue(t, c2.Nodes["n1"], "final", "Z")
	waitValue(t, c2.Nodes["n3"], "final", "Z")
}
