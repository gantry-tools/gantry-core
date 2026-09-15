package replication

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// newNetCluster builds an n-node replicated cluster over the production
// NetTransport (real TCP, authenticated handshake), sharing a memory
// authenticator + authenticated membership as the capability source.
func newNetCluster(t *testing.T, n int) (*Cluster, *MemoryPeerAuthenticator, *MemoryMembership) {
	t.Helper()
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	for i := 0; i < n; i++ {
		id := raft.ServerID(fmt.Sprintf("n%d", i+1))
		secret := "secret-" + string(id)
		caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
		auth.SetSecret(id, secret)
		auth.SetMembership(id, MembershipActive)
		auth.SetReplicationEnabled(id, true)
		auth.SetCapabilities(id, caps)
		membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol, ReplicationEnabled: true})
		nt, err := NewNetTransport(NetTransportOptions{
			ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
			Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: 200 * time.Millisecond, InsecureAllowPlaintext: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = nt.Close() })
		if _, err := c.AddNetNode(id, nt.LocalAddr(), t.TempDir(), false, nt, membership); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < n; i++ {
		id := raft.ServerID(fmt.Sprintf("n%d", i+1))
		joinNetVoter(t, c, id, c.Nodes[id].Address())
	}
	waitLeaderKnown(t, c)
	return c, auth, membership
}

func joinNetVoter(t *testing.T, c *Cluster, id raft.ServerID, addr raft.ServerAddress) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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

func TestNetworkClusterElectionAndPropose(t *testing.T) {
	c, _, _ := newNetCluster(t, 3)
	// Election + AppendEntries over the authenticated TCP transport.
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c.Nodes[id], "foo", "A")
	}
}

func TestNetworkClusterInstallSnapshot(t *testing.T) {
	c, auth, membership := newNetCluster(t, 3)
	writeN(t, c, "adv", 25)
	if err := c.Leader().Snapshot(); err != nil {
		t.Fatal(err)
	}
	// Add a fresh node D with an empty log, sharing the authenticated
	// membership. The leader must stream the snapshot over the authenticated
	// TCP transport, then D replays the trailing log.
	protocol := Version
	id := raft.ServerID("d")
	secret := "secret-d"
	caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
	auth.SetSecret(id, secret)
	auth.SetMembership(id, MembershipActive)
	auth.SetReplicationEnabled(id, true)
	auth.SetCapabilities(id, caps)
	membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol, ReplicationEnabled: true})
	nt, err := NewNetTransport(NetTransportOptions{
		ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: 200 * time.Millisecond, InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nt.Close() })
	if _, err := c.AddNetNode(id, nt.LocalAddr(), t.TempDir(), false, nt, membership); err != nil {
		t.Fatal(err)
	}
	joinNetVoter(t, c, id, c.Nodes[id].Address())

	waitFor(t, 40*time.Second, "fresh node D catches up via snapshot over TCP", func() bool {
		l := c.Leader()
		if l == nil {
			return false
		}
		_, la := l.AppliedIndex()
		_, da := c.Nodes[id].AppliedIndex()
		return da >= la
	})
	waitValue(t, c.Nodes[id], "adv/key-24", "V")
}

func TestNetworkClusterLeaderChange(t *testing.T) {
	c, _, _ := newNetCluster(t, 3)
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))
	before := c.Leader().ID()
	if err := c.Leader().LeadershipTransfer(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "leader changed over the network", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != before
	})
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))
	waitValue(t, c.Nodes["n1"], "bar", "B")
	waitValue(t, c.Nodes["n2"], "bar", "B")
	waitValue(t, c.Nodes["n3"], "bar", "B")
}

func TestNetworkClusterSchemaGateAuthenticated(t *testing.T) {
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	c := NewCluster()
	t.Cleanup(c.CloseAll)
	// Node n1 (old) advertises only schema 1 in its AUTHENTICATED capabilities.
	for i, max := range []int{1, Version, Version} {
		id := raft.ServerID(fmt.Sprintf("n%d", i+1))
		secret := "secret-" + string(id)
		caps := Capabilities{ID: id}
		for v := 1; v <= max; v++ {
			caps.OperationSchemaVersions = append(caps.OperationSchemaVersions, v)
		}
		caps.SnapshotFormatVersions = []int{SnapshotFormatVersion}
		auth.SetSecret(id, secret)
		auth.SetMembership(id, MembershipActive)
		auth.SetReplicationEnabled(id, true)
		auth.SetCapabilities(id, caps)
		membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol, ReplicationEnabled: true})
		nt, err := NewNetTransport(NetTransportOptions{
			ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
			Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: 200 * time.Millisecond, InsecureAllowPlaintext: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = nt.Close() })
		if _, err := c.AddNetNode(id, nt.LocalAddr(), t.TempDir(), false, nt, membership); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < 3; i++ {
		id := raft.ServerID(fmt.Sprintf("n%d", i+1))
		joinNetVoter(t, c, id, c.Nodes[id].Address())
	}
	waitLeaderKnown(t, c)

	// Default ops are schema 2. The authenticated voter n1 only supports schema
	// 1, so schema-2 operations must fail closed at the leader's capability gate
	// (which reads authenticated membership capabilities, not the Fabric).
	if err := proposeMustFail(t, c, kvSet("op-2", "s2", "X", 0)); err == nil {
		t.Fatal("schema-2 op must be blocked while an authenticated voter lacks schema 2")
	}
	if _, _, ok := fsmGet(t, c.Leader(), "s2"); ok {
		t.Fatal("blocked schema-2 op must not be committed")
	}

	// Schema-1 ops remain allowed.
	proposeSuccess(t, c, setSchema(1, "op-1", "s1", "Y", 0))

	// Rolling upgrade: n1 now authenticates schema 2. Schema-2 activates.
	caps2 := c.Nodes["n1"].Capabilities()
	caps2.OperationSchemaVersions = []int{1, Version}
	membership.Set("n1", MembershipStatus{State: MembershipActive, Capabilities: caps2, Protocol: protocol, ReplicationEnabled: true})
	proposeSuccess(t, c, kvSet("op-3", "s2", "X", 0))
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c.Nodes[id], "s2", "X")
	}
}

func TestNetworkClusterMembershipLifecycle(t *testing.T) {
	c, auth, membership := newNetCluster(t, 3)
	proposeSuccess(t, c, kvSet("op-1", "foo", "A", 0))

	// Add a learner D sharing the authenticated membership.
	protocol := Version
	id := raft.ServerID("d")
	secret := "secret-d"
	caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
	auth.SetSecret(id, secret)
	auth.SetMembership(id, MembershipActive)
	auth.SetReplicationEnabled(id, true)
	auth.SetCapabilities(id, caps)
	membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol, ReplicationEnabled: true})
	nt, err := NewNetTransport(NetTransportOptions{
		ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: 200 * time.Millisecond, InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nt.Close() })
	if _, err := c.AddNetNode(id, nt.LocalAddr(), t.TempDir(), false, nt, membership); err != nil {
		t.Fatal(err)
	}
	addr := c.Nodes[id].Address()
	if err := c.Leader().AddNonvoter(id, addr); err != nil {
		t.Fatal(err)
	}
	waitValue(t, c.Nodes[id], "foo", "A")

	// Promote the learner to voter.
	waitFor(t, 20*time.Second, "add nonvoter reflected in config", func() bool {
		cfg, err := c.Leader().Configuration()
		return err == nil && len(cfg) == 4
	})
	if err := c.Leader().AddVoter(id, addr); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "D promoted to voter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == id && s.Suffrage == raft.Voter {
				return true
			}
		}
		return false
	})

	// Demote the voter back to a learner, then remove it safely.
	if err := c.Leader().DemoteVoter(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "D demoted to nonvoter", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == id && s.Suffrage == raft.Nonvoter {
				return true
			}
		}
		return false
	})
	if err := c.Leader().RemoveServer(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "D removed", func() bool {
		cfg, err := c.Leader().Configuration()
		if err != nil {
			return false
		}
		for _, s := range cfg {
			if s.ID == id {
				return false
			}
		}
		return true
	})
	proposeSuccess(t, c, kvSet("op-2", "bar", "B", 0))
}

// TestForwardingAmbiguousResponse exercises the durable operation-ID contract
// under forwarding: a committed operation whose response is lost resolves to
// the same committed result when retried through a different leader, with no
// second mutation.
func TestForwardingAmbiguousResponse(t *testing.T) {
	c := newVoterCluster(t, 3)
	op := kvSet("op-1", "foo", "A", 0)

	// A client writes through a follower; the operation commits and applies.
	first, err := c.Propose(context.Background(), "n2", op)
	if err != nil {
		t.Fatalf("forwarded propose: %v", err)
	}
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c.Nodes[id], "foo", "A")
	}

	// The response is lost; leadership moves to a different node.
	before := c.Leader().ID()
	if err := c.Leader().LeadershipTransfer(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "leadership transferred", func() bool {
		l := c.Leader()
		return l != nil && l.ID() != before
	})
	newLeader := c.Leader()

	// Retry the same operation through the new leader: it must resolve to the
	// existing committed result, not apply a second mutation.
	retry, err := newLeader.Propose(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if retry.OpID != first.OpID || retry.Index != first.Index || retry.Revision != first.Revision {
		t.Fatalf("ambiguous-response retry did not resolve to the committed result: %+v vs %+v", first, retry)
	}
	v, rev, _ := fsmGet(t, newLeader, "foo")
	if v != "A" || rev != 1 {
		t.Fatalf("ambiguous-response retry mutated state: %q rev=%d", v, rev)
	}
}
