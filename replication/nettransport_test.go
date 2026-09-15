package replication

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// rawHandshake dials the transport and performs a handshake with the given
// request, returning the connection and the full (possibly rejected) response.
func rawHandshake(t *testing.T, addr raft.ServerAddress, req *AuthRequest) (net.Conn, handshakeResponse) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", string(addr), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	body, _ := json.Marshal(req)
	if err := writeFrame(conn, frameAuth, body); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	typ, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read auth resp: %v", err)
	}
	if typ != frameAuthResp {
		t.Fatalf("unexpected frame %d", typ)
	}
	var hr handshakeResponse
	if err := json.Unmarshal(payload, &hr); err != nil {
		t.Fatalf("decode auth resp: %v", err)
	}
	return conn, hr
}

func baseAuthReq(nodeID raft.ServerID, secret string, protocol int, caps Capabilities) *AuthRequest {
	req, err := buildHandshake(nodeID, nodeID, raft.ServerAddress("127.0.0.1:1"), protocol, caps, secret, "nonce-1", time.Now())
	if err != nil {
		panic(err)
	}
	return req
}

// authReq builds a handshake with a caller-chosen nonce (freshness for replay
// tests), so the signature is computed over the actual nonce used.
func authReq(nodeID raft.ServerID, secret string, protocol int, caps Capabilities, nonce string) *AuthRequest {
	req, err := buildHandshake(nodeID, nodeID, raft.ServerAddress("127.0.0.1:1"), protocol, caps, secret, nonce, time.Now())
	if err != nil {
		panic(err)
	}
	return req
}

func newTestAuth(t *testing.T, nodes ...raft.ServerID) (*MemoryPeerAuthenticator, *MemoryMembership, int) {
	t.Helper()
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	for _, id := range nodes {
		caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
		auth.SetSecret(id, "secret-"+string(id))
		auth.SetMembership(id, MembershipActive)
		auth.SetReplicationEnabled(id, true)
		auth.SetCapabilities(id, caps)
		membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol, ReplicationEnabled: true})
	}
	return auth, membership, protocol
}

func TestNetTransportRequiresTLSOrExplicitInsecure(t *testing.T) {
	auth, membership, protocol := newTestAuth(t, "n1")
	if _, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: "s", Protocol: protocol, Capabilities: Capabilities{ID: "n1"},
	}); err == nil {
		t.Fatal("plaintext transport must be rejected without explicit insecure opt-in")
	}
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: "s", Protocol: protocol, Capabilities: Capabilities{ID: "n1"},
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatalf("explicit insecure mode must be allowed: %v", err)
	}
	nt.Close()
}

func TestNetTransportHandshakeAuth(t *testing.T) {
	auth, _, protocol := newTestAuth(t, "n1")
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Protocol: protocol,
		Secret: "secret-n1", Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	addr := nt.LocalAddr()

	// Valid handshake is allowed, and the server proves its own identity.
	conn, hr := rawHandshake(t, addr, baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"}))
	if !hr.Result.Allowed {
		t.Fatalf("valid handshake rejected: %s", hr.Result.Reason)
	}
	if hr.ServerAuth == nil {
		t.Fatal("server must reciprocate with its own identity proof")
	}
	if err := auth.VerifyPeer(context.Background(), "n1", *hr.ServerAuth); err != nil {
		t.Fatalf("server identity proof failed: %v", err)
	}
	_ = conn.Close()

	// Wrong secret is rejected.
	_, hr = rawHandshake(t, addr, baseAuthReq("n1", "wrong", protocol, Capabilities{ID: "n1"}))
	if hr.Result.Allowed {
		t.Fatal("wrong-secret handshake must be rejected")
	}

	// Claiming a different raft identity than the authenticated node is rejected.
	bad := baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"})
	bad.RaftServerID = "n9"
	_, hr = rawHandshake(t, addr, bad)
	if hr.Result.Allowed {
		t.Fatal("mismatched raft identity must be rejected")
	}

	// Incompatible protocol is rejected.
	_, hr = rawHandshake(t, addr, baseAuthReq("n1", "secret-n1", protocol+1, Capabilities{ID: "n1"}))
	if hr.Result.Allowed {
		t.Fatal("incompatible protocol must be rejected")
	}

	// Stale timestamp (replay) is rejected.
	stale := baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"})
	stale.Timestamp = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	_, hr = rawHandshake(t, addr, stale)
	if hr.Result.Allowed {
		t.Fatal("stale handshake must be rejected")
	}

	// Revoked membership is rejected.
	auth.SetMembership("n1", MembershipRevoked)
	_, hr = rawHandshake(t, addr, baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"}))
	if hr.Result.Allowed {
		t.Fatal("revoked member handshake must be rejected")
	}
}

func TestNetTransportHandshakeReplay(t *testing.T) {
	auth, _, protocol := newTestAuth(t, "n1")
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Protocol: protocol,
		Secret: "secret-n1", Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	addr := nt.LocalAddr()

	req := baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"})

	// First valid handshake is accepted and consumes the nonce.
	conn, hr := rawHandshake(t, addr, req)
	if !hr.Result.Allowed {
		t.Fatalf("first handshake rejected: %s", hr.Result.Reason)
	}
	_ = conn.Close()

	// Exact replay of the same signed handshake (same nonce, same timestamp)
	// must be rejected as a replay.
	_, hr = rawHandshake(t, addr, req)
	if hr.Result.Allowed {
		t.Fatal("exact handshake replay must be rejected")
	}

	// Same nonce but a modified body: the signature fails closed.
	tampered := *req
	tampered.RaftServerAddr = "127.0.0.1:9"
	_, hr = rawHandshake(t, addr, &tampered)
	if hr.Result.Allowed {
		t.Fatal("tampered handshake must be rejected")
	}

	// A fresh nonce with a valid signature is accepted again.
	fresh := authReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"}, "nonce-2")
	conn, hr = rawHandshake(t, addr, fresh)
	if !hr.Result.Allowed {
		t.Fatalf("fresh-nonce handshake rejected: %s", hr.Result.Reason)
	}
	_ = conn.Close()
}

func TestNetTransportMutualAuthentication(t *testing.T) {
	auth, _, protocol := newTestAuth(t, "n1", "n2")
	// A transport for n1; n2 dials it.
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Protocol: protocol,
		Secret: "secret-n1", Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()

	// The server proves n1's identity to the dialer.
	conn, hr := rawHandshake(t, nt.LocalAddr(), baseAuthReq("n2", "secret-n2", protocol, Capabilities{ID: "n2"}))
	if !hr.Result.Allowed {
		t.Fatalf("handshake rejected: %s", hr.Result.Reason)
	}
	// Dialer expects n1: the server identity proof verifies.
	if err := auth.VerifyPeer(context.Background(), "n1", *hr.ServerAuth); err != nil {
		t.Fatalf("expected-peer verification failed: %v", err)
	}
	// A dialer that expects a DIFFERENT node must reject: the endpoint proved
	// it is n1, not the expected n2.
	if err := auth.VerifyPeer(context.Background(), "n2", *hr.ServerAuth); err == nil {
		t.Fatal("expected-peer / actual-peer identity mismatch must be rejected")
	}
	_ = conn.Close()
}

func TestNetTransportRejectsUnauthenticatedEndpoint(t *testing.T) {
	auth, _, protocol := newTestAuth(t, "n1")
	// A raw listener that accepts a handshake but provides no server identity
	// proof (not a Gantry replication endpoint).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		typ, _, err := readFrame(conn)
		if err != nil || typ != frameAuth {
			return
		}
		// Respond "allowed" but with no reciprocal identity proof.
		out, _ := json.Marshal(handshakeResponse{Result: AuthResult{Allowed: true}})
		_ = writeFrame(conn, frameAuthResp, out)
	}()

	// A dialing transport must reject an endpoint that provides no identity proof.
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Protocol: protocol,
		Secret: "secret-n1", Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	if _, err := nt.getConn(context.Background(), raft.ServerAddress(ln.Addr().String()), "n1"); err == nil {
		t.Fatal("dialing an unauthenticated endpoint must fail")
	}
}

func TestNetTransportMalformedFrameAndCompatibilityRevalidation(t *testing.T) {
	auth, membership, protocol := newTestAuth(t, "n1")
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: "secret-n1", Protocol: protocol, Capabilities: Capabilities{ID: "n1"},
		RevalidateEvery: 100 * time.Millisecond, InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	addr := nt.LocalAddr()

	// Malformed frame after handshake closes the connection promptly.
	conn, hr := rawHandshake(t, addr, baseAuthReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"}))
	if !hr.Result.Allowed {
		t.Fatalf("handshake failed: %s", hr.Result.Reason)
	}
	if _, err := conn.Write([]byte{0xff, 0x00, 0x00, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'}); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	start := time.Now()
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected connection to close after a malformed frame")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("malformed frame did not close the connection promptly: %v", elapsed)
	}
	_ = conn.Close()

	// Active connections must terminate when the authenticated membership
	// becomes revoked, disabled, removed (unknown), protocol-incompatible, or
	// replication no longer authorized.
	cases := []struct {
		name   string
		mutate func()
	}{
		{"revoked", func() {
			membership.Set("n1", MembershipStatus{State: MembershipRevoked, Capabilities: Capabilities{ID: "n1"}, Protocol: protocol, ReplicationEnabled: true})
		}},
		{"disabled", func() {
			membership.Set("n1", MembershipStatus{State: MembershipDisabled, Capabilities: Capabilities{ID: "n1"}, Protocol: protocol, ReplicationEnabled: true})
		}},
		{"removed", func() { membership.Set("n1", MembershipStatus{}) }}, // unknown -> lookup error
		{"protocol incompatible", func() {
			membership.Set("n1", MembershipStatus{State: MembershipActive, Capabilities: Capabilities{ID: "n1"}, Protocol: protocol + 1, ReplicationEnabled: true})
		}},
		{"replication not authorized", func() {
			membership.Set("n1", MembershipStatus{State: MembershipActive, Capabilities: Capabilities{ID: "n1"}, Protocol: protocol, ReplicationEnabled: false})
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, hr := rawHandshake(t, addr, authReq("n1", "secret-n1", protocol, Capabilities{ID: "n1"}, fmt.Sprintf("reval-nonce-%d", i)))
			if !hr.Result.Allowed {
				t.Fatalf("handshake failed: %s", hr.Result.Reason)
			}
			tc.mutate()
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 1)
			start := time.Now()
			for time.Now().Before(start.Add(2 * time.Second)) {
				if _, err := conn.Read(buf); err != nil {
					return // connection terminated
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("active connection was not terminated after %s", tc.name)
		})
	}
}

func TestNetTransportRejectsUnknownNode(t *testing.T) {
	auth, _, protocol := newTestAuth(t)
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Protocol: protocol,
		Secret: "s", Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
		InsecureAllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	_, hr := rawHandshake(t, nt.LocalAddr(), baseAuthReq("ghost", "s", protocol, Capabilities{ID: "ghost"}))
	if hr.Result.Allowed {
		t.Fatal("unknown node must be rejected")
	}
	_ = fmt.Sprintf
}
