package replication

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// TestRevokeStopsReplicationDelivery freezes the peer revocation contract: a
// peer disabled/revoked in the dialer's local membership view is refused for
// NEW replication delivery (dial-time membership check) and the established
// A->B connection is dropped by revalidation, so B stops advancing while the
// healthy quorum continues. Re-enabling B lets it reconnect and catch up.
func TestRevokeStopsReplicationDelivery(t *testing.T) {
	c, _, membership := newNetCluster(t, 3)
	proposeSuccess(t, c, kvSet("op-rev1", "rev", "pre", 0))
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		waitValue(t, c.Nodes[id], "rev", "pre")
	}

	// Disable follower B (n2) in the shared membership view. The leader (A) is
	// the dialer, so this exercises the dial-time membership check + the
	// established A->B revalidation (RevalidateEvery 200ms).
	membership.Set("n2", MembershipStatus{
		State: MembershipDisabled, Protocol: Version, ReplicationEnabled: true,
		Capabilities: c.Nodes["n2"].Capabilities(),
	})
	time.Sleep(500 * time.Millisecond) // let revalidation drop A->B

	// A commit must still succeed (A+C quorum) but must NOT reach B.
	proposeSuccess(t, c, kvSet("op-rev2", "rev2", "post", 0))
	waitValue(t, c.Nodes["n1"], "rev2", "post")
	waitValue(t, c.Nodes["n3"], "rev2", "post")
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := c.Nodes["n2"].FSM().(*KVFSM).Get("rev2"); ok {
			t.Fatal("disabled peer received replicated state")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Re-enable B: it must reconnect (new dial allowed) and catch up.
	membership.Set("n2", MembershipStatus{
		State: MembershipActive, Protocol: Version, ReplicationEnabled: true,
		Capabilities: c.Nodes["n2"].Capabilities(),
	})
	waitValue(t, c.Nodes["n2"], "rev2", "post")
	_ = context.Background
}
