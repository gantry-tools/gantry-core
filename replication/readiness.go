package replication

import (
	"context"
	"strconv"
	"time"

	"github.com/hashicorp/raft"
)

// HealthHook reports product health (e.g. a product FSM's committed-apply
// failure). A non-nil error drives readiness to Unhealthy (sticky while
// present); Core does not know the product's failure implementation.
type HealthHook func() error

// ReadinessDriver derives runtime readiness from the real raft lifecycle plus
// a product health hook. It never promotes merely because the raft process
// exists: recovery and catch-up must actually be established first.
type ReadinessDriver struct {
	node     *Node
	set      func(Readiness)
	health   HealthHook
	interval time.Duration
}

// NewReadinessDriver returns a readiness driver over node. set receives every
// derived readiness; health, when non-nil, is polled each cycle and a non-nil
// result sets ReadinessUnhealthy. interval is the derivation cadence.
func NewReadinessDriver(node *Node, set func(Readiness), health HealthHook, interval time.Duration) *ReadinessDriver {
	if interval <= 0 {
		interval = 150 * time.Millisecond
	}
	return &ReadinessDriver{node: node, set: set, health: health, interval: interval}
}

// Run blocks until ctx is done, deriving readiness each cycle. On ctx.Done it
// sets ReadinessShuttingDown (reject new authoritative mutations) and returns.
func (d *ReadinessDriver) Run(ctx context.Context) {
	tick := time.NewTicker(d.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			d.set(ReadinessShuttingDown)
			return
		case <-tick.C:
		}
		if d.health != nil {
			if err := d.health(); err != nil {
				d.set(ReadinessUnhealthy)
				continue
			}
		}
		switch d.node.State() {
		case raft.Leader:
			d.set(ReadinessReadyLeader)
		case raft.Follower:
			addr, _ := d.node.Leader()
			switch {
			case addr != "" && d.caughtUp():
				d.set(ReadinessReadyFollower)
			case addr != "":
				d.set(ReadinessLearner)
			default:
				d.set(ReadinessNoLeader)
			}
		case raft.Candidate:
			d.set(ReadinessNoLeader)
		case raft.Shutdown:
			d.set(ReadinessShuttingDown)
		}
	}
}

// caughtUp reports whether the follower has processed its entire raft log
// (raft's own applied index reached its last log index; config changes and
// no-ops count here, unlike the product FSM's applied index) and has recent
// leader contact.
func (d *ReadinessDriver) caughtUp() bool {
	stats := d.node.Stats()
	applied := parseIndex(stats["applied_index"])
	last := parseIndex(stats["last_log_index"])
	if applied < last {
		return false
	}
	switch contact := stats["last_contact"]; contact {
	case "0":
		return true
	case "", "never":
		return false
	default:
		since, err := time.ParseDuration(contact)
		if err != nil {
			return false
		}
		return since < 2*time.Second
	}
}

func parseIndex(v string) uint64 {
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}
