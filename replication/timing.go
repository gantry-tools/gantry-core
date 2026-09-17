package replication

import "time"

// Timing holds the raft timing parameters for a node. Products compose a node
// from a Timing profile; the values materially affect stability over real
// inter-host / WAN links (a leader whose heartbeats fall outside followers'
// election windows causes constant re-elections).
type Timing struct {
	HeartbeatTimeout   time.Duration
	ElectionTimeout    time.Duration
	LeaderLeaseTimeout time.Duration
	CommitTimeout      time.Duration
	ProposeTimeout     time.Duration
}

// LocalTestTiming returns the aggressive timing used by deterministic local
// tests (fast elections). It is NOT suitable for production WAN links.
func LocalTestTiming() Timing {
	return Timing{
		HeartbeatTimeout:   250 * time.Millisecond,
		ElectionTimeout:    500 * time.Millisecond,
		LeaderLeaseTimeout: 250 * time.Millisecond,
		CommitTimeout:      20 * time.Millisecond,
		ProposeTimeout:     3 * time.Second,
	}
}

// ProductionTiming returns production-safe defaults for real inter-host and
// WAN replication links: leaders' heartbeats stay comfortably inside
// followers' election windows under realistic cross-region latency, so a
// healthy leader is not spuriously deposed. Products may override individual
// values via operator configuration.
func ProductionTiming() Timing {
	return Timing{
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    3 * time.Second,
		LeaderLeaseTimeout: 750 * time.Millisecond,
		CommitTimeout:      100 * time.Millisecond,
		ProposeTimeout:     10 * time.Second,
	}
}
