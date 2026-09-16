// Package replication authority primitives: the product-neutral configured-mode
// + runtime-readiness authority, the authoritative mutation routing matrix,
// caller/request operation-identity propagation, and the application-layer
// follower-forwarding envelope. Products (Watchpost, Trestle, ...) supply their
// own semantic operation construction and materialization; this package owns
// only the generic mechanics.
package replication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Mode is the configured operating mode of a replicated node. It is the
// authoritative product configuration (never inferred from membership); a
// configured replicated node never falls back to the product's local path when
// replication is unavailable.
type Mode int

const (
	ModeStandalone Mode = iota
	ModeReplicated
)

func (m Mode) String() string {
	if m == ModeReplicated {
		return "replicated"
	}
	return "standalone"
}

// Readiness is the runtime replicated-readiness of a node, distinct from the
// configured mode: a configured replicated node that is not ReadyLeader /
// ReadyFollower MUST reject authoritative mutations (never fail open to the
// product's local path).
type Readiness int

const (
	ReadinessStarting Readiness = iota
	ReadinessLearner
	ReadinessReadyFollower
	ReadinessReadyLeader
	ReadinessNoLeader
	ReadinessUnhealthy
	ReadinessShuttingDown
)

func (r Readiness) String() string {
	switch r {
	case ReadinessStarting:
		return "starting"
	case ReadinessLearner:
		return "learner/catching-up"
	case ReadinessReadyFollower:
		return "ready-follower"
	case ReadinessReadyLeader:
		return "ready-leader"
	case ReadinessNoLeader:
		return "no-leader"
	case ReadinessUnhealthy:
		return "unhealthy"
	case ReadinessShuttingDown:
		return "shutting-down"
	}
	return "unknown"
}

// ErrStandalone is returned when the authoritative mutation router is consulted
// while the node is configured standalone: the caller must use the product's
// existing local mutation path.
var ErrStandalone = errors.New("replication: configured standalone; use the product local mutation path")

// ForwardRequest is the authenticated application-layer follower-forwarding
// envelope: the operation identity (the caller's request identity) plus the
// product's semantic intent bytes. The product defines Kind/ObjectID semantics
// and the payload shape; this package transports the envelope unchanged.
type ForwardRequest struct {
	Kind     string          `json:"kind"`
	ObjectID string          `json:"object_id"`
	OpID     string          `json:"op_id"`
	Revision int64           `json:"revision,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// ForwardClient is the authenticated Gantry application-RPC forwarding
// transport from a ready follower to the current leader. The live wiring
// installs it; the deterministic harness routes to the leader's controller.
type ForwardClient func(ctx context.Context, req ForwardRequest) (*ApplyResult, error)

// requestIDKey is the context key carrying the caller-supplied
// idempotency/request identity that maps to the replication operation ID.
type requestIDKey struct{}

// WithRequestID attaches a caller-supplied idempotency/request identity to ctx.
// Retrying the SAME logical mutation with the SAME identity resolves to the
// SAME replication operation identity: the durable op-ID contract then returns
// the original committed result (no second mutation) for identical semantic
// payloads and fails closed for a changed payload. A genuinely new intent uses
// a fresh identity. Absent identity -> the product generates a fresh operation
// ID per intent.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the caller-supplied idempotency/request identity, or ""
// when the caller provided none.
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// Authority is the product-neutral configured-mode + runtime-readiness state
// and the authoritative mutation routing matrix. Products drive mode/readiness
// and supply the local proposal executor and the follower-forward action; this
// type decides which path is legal and NEVER falls back to the product local
// path for a configured replicated node that is not ready.
type Authority struct {
	mu        sync.Mutex
	mode      Mode
	readiness Readiness
	forward   ForwardClient
}

// NewAuthority returns a mutation authority starting as configured standalone
// at readiness starting; production wiring sets the mode and drives readiness.
func NewAuthority() *Authority {
	return &Authority{mode: ModeStandalone, readiness: ReadinessStarting}
}

// SetMode sets the configured operating mode.
func (a *Authority) SetMode(m Mode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = m
}

// SetReadiness sets the runtime readiness.
func (a *Authority) SetReadiness(r Readiness) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.readiness = r
}

// SetForwardClient installs the authenticated application-RPC forwarding
// transport used by ready-follower mutations.
func (a *Authority) SetForwardClient(fn ForwardClient) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forward = fn
}

// ForwardClient returns the installed forwarding transport (nil when none).
func (a *Authority) ForwardClient() ForwardClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.forward
}

// State returns the configured mode and runtime readiness.
func (a *Authority) State() (Mode, Readiness) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode, a.readiness
}

// Route applies the authoritative mutation matrix. For a ready leader it calls
// propose; for a ready follower it calls forward; a configured replicated node
// in any other readiness rejects. It NEVER falls back to the product local path
// for a configured replicated node.
func (a *Authority) Route(ctx context.Context, propose, forward func(context.Context) (*ApplyResult, error)) (*ApplyResult, error) {
	a.mu.Lock()
	mode, rd := a.mode, a.readiness
	a.mu.Unlock()
	if mode == ModeStandalone {
		return nil, ErrStandalone
	}
	switch rd {
	case ReadinessReadyLeader:
		return propose(ctx)
	case ReadinessReadyFollower:
		return forward(ctx)
	default:
		return nil, fmt.Errorf("replication mutation unavailable (readiness %s)", rd)
	}
}
