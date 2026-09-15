package replication

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// MembershipState is the authenticated membership state of a replication peer.
type MembershipState int

const (
	MembershipActive MembershipState = iota
	MembershipDisabled
	MembershipRevoked
)

// MembershipStatus is the authenticated membership + capability state of a
// node, returned by a MembershipChecker. In production this derives from the
// Gantry cluster membership relationship (cluster_members), not an in-memory
// test registry. ReplicationEnabled is the transport-level authorization the
// membership relationship grants; State is its lifecycle.
type MembershipStatus struct {
	State              MembershipState
	Capabilities       Capabilities
	Protocol           int
	ReplicationEnabled bool
}

// PeerAuthenticator verifies connection-establishment handshakes and binds
// the authenticated Gantry node identity (raft.ServerID) to the connection in
// BOTH directions. A peer must not be able to authenticate as one Gantry node
// while presenting a different Raft server identity.
//
// Replay contract: implementations MUST reject a handshake whose nonce was
// already consumed by the same authenticated node while the nonce is within
// the freshness window, so a captured valid handshake cannot be replayed. The
// reference MemoryPeerAuthenticator keeps an in-memory consumed-nonce store
// that expires after the skew window; production (Watchpost) MUST use its
// persistent cluster nonce/replay machinery (cluster_nonces) or an equivalent
// durable adapter so replay protection survives restart.
type PeerAuthenticator interface {
	// Authenticate is the accepting side: verify a dialer's identity proof,
	// consume its nonce (replay protection), and return whether it is allowed.
	Authenticate(ctx context.Context, in AuthRequest) (AuthResult, error)

	// VerifyPeer is the dialing side: verify the accepting node's reciprocal
	// identity proof against the expected node identity, consuming its nonce.
	// A connection must fail if the network endpoint presents a valid
	// credential for some other Gantry node.
	VerifyPeer(ctx context.Context, expected raft.ServerID, in AuthRequest) error
}

// MembershipChecker supplies authenticated membership/capability state for
// periodic revalidation and for production schema gating (replacing the test
// Fabric as the capability source).
type MembershipChecker interface {
	Membership(ctx context.Context, nodeID raft.ServerID) (MembershipStatus, error)
}

// CapabilitySource supplies authenticated capabilities for schema gating.
// Unknown or unauthenticated capability data must fail closed.
type CapabilitySource interface {
	CapabilitiesOf(id raft.ServerID) (Capabilities, bool)
}

// AuthRequest is the connection-establishment handshake. The signature binds
// the whole body (node identity, raft identity, capabilities, protocol) to the
// presented Gantry peer credential, so a connection is bound to exactly one
// authenticated node.
type AuthRequest struct {
	NodeID         raft.ServerID      `json:"node_id"`
	RaftServerID   raft.ServerID      `json:"raft_server_id"`
	RaftServerAddr raft.ServerAddress `json:"raft_server_addr"`
	Protocol       int                `json:"protocol"`
	Capabilities   Capabilities       `json:"capabilities"`
	Timestamp      string             `json:"timestamp"` // RFC3339Nano
	Nonce          string             `json:"nonce"`
	Signature      string             `json:"signature"`
}

// AuthResult is the response to a handshake.
type AuthResult struct {
	Allowed       bool               `json:"allowed"`
	ServerAddress raft.ServerAddress `json:"server_address,omitempty"`
	Reason        string             `json:"reason,omitempty"`
}

// authBody is the canonical serialization the signature covers.
type authBody struct {
	NodeID         raft.ServerID      `json:"node_id"`
	RaftServerID   raft.ServerID      `json:"raft_server_id"`
	RaftServerAddr raft.ServerAddress `json:"raft_server_addr"`
	Protocol       int                `json:"protocol"`
	Capabilities   Capabilities       `json:"capabilities"`
}

// HandshakeURIMethod and capability used for the handshake signature, reusing
// the Gantry request-signing vocabulary (cluster package semantics) so the
// connection-establishment handshake is not a second identity system.
const (
	handshakeMethod     = "REPLICATE"
	handshakePath       = "/replicate/handshake"
	handshakeCapability = "replication"
	handshakeSkewWindow = 5 * time.Minute
)

// buildHandshake signs an auth body with the peer's Gantry outbound secret.
func buildHandshake(nodeID, raftID raft.ServerID, addr raft.ServerAddress, protocol int, caps Capabilities, secret, nonce string, now time.Time) (*AuthRequest, error) {
	body, err := json.Marshal(authBody{NodeID: nodeID, RaftServerID: raftID, RaftServerAddr: addr, Protocol: protocol, Capabilities: caps})
	if err != nil {
		return nil, err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	sig := handshakeSignature(secret, timestamp, nonce, nodeID, body)
	return &AuthRequest{
		NodeID: nodeID, RaftServerID: raftID, RaftServerAddr: addr,
		Protocol: protocol, Capabilities: caps, Timestamp: timestamp, Nonce: nonce, Signature: sig,
	}, nil
}

func handshakeSignature(secret, timestamp, nonce string, requestID raft.ServerID, body []byte) string {
	return base64.RawURLEncoding.EncodeToString(handshakeSignatureBytes(secret, timestamp, nonce, requestID, body))
}

func handshakeSignatureBytes(secret, timestamp, nonce string, requestID raft.ServerID, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s\n%s\n%s\n%x", handshakeMethod, handshakePath, timestamp, nonce, requestID, handshakeCapability, sha256.Sum256(body))
	return mac.Sum(nil)
}

func verifyHandshakeSignature(secret, timestamp, nonce string, requestID raft.ServerID, body []byte, sig string) bool {
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, handshakeSignatureBytes(secret, timestamp, nonce, requestID, body))
}

// MemoryPeerAuthenticator is a reference/test PeerAuthenticator backed by
// per-node shared secrets and membership states. It provides in-process
// handshake replay protection (consumed-nonce store that expires after the
// freshness window). Products implement the same interface over the Gantry
// cluster_members relationship, and MUST provide durable replay state.
type MemoryPeerAuthenticator struct {
	mu       sync.Mutex
	secrets  map[raft.ServerID]string
	states   map[raft.ServerID]MembershipState
	enabled  map[raft.ServerID]bool
	caps     map[raft.ServerID]Capabilities
	seen     map[raft.ServerID]map[string]time.Time
	protocol int
	now      func() time.Time
}

// NewMemoryPeerAuthenticator creates an empty reference authenticator.
func NewMemoryPeerAuthenticator(protocol int) *MemoryPeerAuthenticator {
	return &MemoryPeerAuthenticator{
		secrets:  make(map[raft.ServerID]string),
		states:   make(map[raft.ServerID]MembershipState),
		enabled:  make(map[raft.ServerID]bool),
		caps:     make(map[raft.ServerID]Capabilities),
		seen:     make(map[raft.ServerID]map[string]time.Time),
		protocol: protocol,
		now:      time.Now,
	}
}

// SetSecret records a peer's outbound secret.
func (m *MemoryPeerAuthenticator) SetSecret(id raft.ServerID, secret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[id] = secret
}

// SetMembership records a peer's membership state.
func (m *MemoryPeerAuthenticator) SetMembership(id raft.ServerID, state MembershipState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[id] = state
}

// SetReplicationEnabled grants/revokes the transport-level replication
// authorization for a peer.
func (m *MemoryPeerAuthenticator) SetReplicationEnabled(id raft.ServerID, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled[id] = enabled
}

// SetCapabilities records a peer's advertised capabilities.
func (m *MemoryPeerAuthenticator) SetCapabilities(id raft.ServerID, c Capabilities) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caps[id] = c
}

// consumeNonce rejects a replayed nonce for the same authenticated node and
// records a fresh one with an expiry bounded by the freshness window. Expired
// entries are pruned on every call, so the store is bounded.
func (m *MemoryPeerAuthenticator) consumeNonce(nodeID raft.ServerID, nonce string, now time.Time) error {
	cutoff := now.Add(-handshakeSkewWindow)
	seen := m.seen[nodeID]
	for n, exp := range seen {
		if exp.Before(cutoff) {
			delete(seen, n)
		}
	}
	if _, ok := seen[nonce]; ok {
		return errors.New("handshake nonce replay rejected")
	}
	if seen == nil {
		seen = make(map[string]time.Time)
	}
	seen[nonce] = now.Add(handshakeSkewWindow)
	m.seen[nodeID] = seen
	return nil
}

// verifyHandshakeCommon validates identity binding, protocol, membership,
// freshness, signature and nonce replay for an auth request against a node's
// secret. It is shared by the accepting and dialing sides.
func (m *MemoryPeerAuthenticator) verifyHandshakeCommon(in AuthRequest) error {
	if in.RaftServerID != in.NodeID {
		return errors.New("raft server identity does not match authenticated node identity")
	}
	if in.Protocol != m.protocol {
		return errors.New("incompatible replication protocol")
	}
	m.mu.Lock()
	secret, hasSecret := m.secrets[in.NodeID]
	state, hasState := m.states[in.NodeID]
	enabled, hasEnabled := m.enabled[in.NodeID]
	m.mu.Unlock()
	if !hasSecret || !hasState || !hasEnabled {
		return errors.New("replication member unknown")
	}
	if state != MembershipActive {
		return errors.New("replication member not active")
	}
	if !enabled {
		return errors.New("replication not authorized for member")
	}
	at, err := time.Parse(time.RFC3339Nano, in.Timestamp)
	if err != nil {
		return errors.New("invalid handshake timestamp")
	}
	now := m.now()
	if at.Before(now.Add(-handshakeSkewWindow)) || at.After(now.Add(handshakeSkewWindow)) {
		return errors.New("handshake outside clock-skew window")
	}
	body, err := json.Marshal(authBody{NodeID: in.NodeID, RaftServerID: in.RaftServerID, RaftServerAddr: in.RaftServerAddr, Protocol: in.Protocol, Capabilities: in.Capabilities})
	if err != nil {
		return err
	}
	if !verifyHandshakeSignature(secret, in.Timestamp, in.Nonce, in.NodeID, body, in.Signature) {
		return errors.New("invalid replication handshake signature")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.consumeNonce(in.NodeID, in.Nonce, now)
}

// Authenticate implements PeerAuthenticator (accepting side).
func (m *MemoryPeerAuthenticator) Authenticate(ctx context.Context, in AuthRequest) (AuthResult, error) {
	if err := m.verifyHandshakeCommon(in); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Allowed: true, ServerAddress: in.RaftServerAddr}, nil
}

// VerifyPeer implements PeerAuthenticator (dialing side): the accepting node
// must prove it is exactly the expected Gantry node / raft.ServerID.
func (m *MemoryPeerAuthenticator) VerifyPeer(ctx context.Context, expected raft.ServerID, in AuthRequest) error {
	if expected == "" {
		return errors.New("expected peer identity is empty")
	}
	if in.NodeID != expected || in.RaftServerID != expected {
		return fmt.Errorf("peer authenticated as %q but expected %q", in.NodeID, expected)
	}
	return m.verifyHandshakeCommon(in)
}

// MemoryMembership is a reference/test MembershipChecker + CapabilitySource.
type MemoryMembership struct {
	mu    sync.Mutex
	state map[raft.ServerID]MembershipStatus
}

// NewMemoryMembership creates an empty reference membership store.
func NewMemoryMembership() *MemoryMembership {
	return &MemoryMembership{state: make(map[raft.ServerID]MembershipStatus)}
}

// Set records a node's membership status.
func (m *MemoryMembership) Set(id raft.ServerID, st MembershipStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[id] = st
}

// Membership implements MembershipChecker.
func (m *MemoryMembership) Membership(ctx context.Context, id raft.ServerID) (MembershipStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[id]
	if !ok {
		return MembershipStatus{}, errors.New("replication member unknown")
	}
	return st, nil
}

// CapabilitiesOf implements CapabilitySource from authenticated membership state.
func (m *MemoryMembership) CapabilitiesOf(id raft.ServerID) (Capabilities, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[id]
	if !ok {
		return Capabilities{}, false
	}
	return st.Capabilities, true
}

var (
	_ PeerAuthenticator = (*MemoryPeerAuthenticator)(nil)
	_ MembershipChecker = (*MemoryMembership)(nil)
	_ CapabilitySource  = (*MemoryMembership)(nil)
)
