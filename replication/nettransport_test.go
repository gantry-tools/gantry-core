package replication

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// rawHandshake dials the transport and performs a handshake with the given
// request, returning the connection and result.
func rawHandshake(t *testing.T, addr raft.ServerAddress, req *AuthRequest) (net.Conn, AuthResult) {
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
	var res AuthResult
	if err := json.Unmarshal(payload, &res); err != nil {
		t.Fatalf("decode auth resp: %v", err)
	}
	return conn, res
}

func baseAuthReq(nodeID raft.ServerID, secret string, protocol int, caps Capabilities) *AuthRequest {
	req, err := buildHandshake(nodeID, nodeID, raft.ServerAddress("127.0.0.1:1"), protocol, caps, secret, "nonce-1", time.Now())
	if err != nil {
		panic(err)
	}
	return req
}

func TestNetTransportHandshakeAuth(t *testing.T) {
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	id := raft.ServerID("n1")
	secret := "secret-n1"
	caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
	auth.SetSecret(id, secret)
	auth.SetMembership(id, MembershipActive)
	auth.SetCapabilities(id, caps)
	membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol})

	nt, err := NewNetTransport(NetTransportOptions{
		ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	addr := nt.LocalAddr()

	// Valid handshake is allowed.
	conn, res := rawHandshake(t, addr, baseAuthReq(id, secret, protocol, caps))
	if !res.Allowed {
		t.Fatalf("valid handshake rejected: %s", res.Reason)
	}
	_ = conn.Close()

	// Wrong secret is rejected.
	_, res = rawHandshake(t, addr, baseAuthReq(id, "wrong-secret", protocol, caps))
	if res.Allowed {
		t.Fatal("wrong-secret handshake must be rejected")
	}

	// Claiming a different raft identity than the authenticated node is rejected.
	bad := baseAuthReq(id, secret, protocol, caps)
	bad.RaftServerID = "n9"
	_, res = rawHandshake(t, addr, bad)
	if res.Allowed {
		t.Fatal("mismatched raft identity must be rejected")
	}

	// Incompatible protocol is rejected.
	_, res = rawHandshake(t, addr, baseAuthReq(id, secret, protocol+1, caps))
	if res.Allowed {
		t.Fatal("incompatible protocol must be rejected")
	}

	// Stale timestamp (replay) is rejected.
	stale := baseAuthReq(id, secret, protocol, caps)
	stale.Timestamp = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	_, res = rawHandshake(t, addr, stale)
	if res.Allowed {
		t.Fatal("stale handshake must be rejected")
	}

	// Revoked membership is rejected.
	auth.SetMembership(id, MembershipRevoked)
	_, res = rawHandshake(t, addr, baseAuthReq(id, secret, protocol, caps))
	if res.Allowed {
		t.Fatal("revoked member handshake must be rejected")
	}
}

func TestNetTransportMalformedFrameAndRevalidation(t *testing.T) {
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	id := raft.ServerID("n1")
	secret := "secret-n1"
	caps := Capabilities{ID: id, OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
	auth.SetSecret(id, secret)
	auth.SetMembership(id, MembershipActive)
	auth.SetCapabilities(id, caps)
	membership.Set(id, MembershipStatus{State: MembershipActive, Capabilities: caps, Protocol: protocol})

	nt, err := NewNetTransport(NetTransportOptions{
		ID: id, Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: secret, Protocol: protocol, Capabilities: caps, RevalidateEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	addr := nt.LocalAddr()

	// Malformed frame after handshake closes the connection promptly.
	conn, res := rawHandshake(t, addr, baseAuthReq(id, secret, protocol, caps))
	if !res.Allowed {
		t.Fatalf("handshake failed: %s", res.Reason)
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

	// Server-side revalidation terminates an active connection promptly when
	// membership leaves the active state (authenticated membership, not the
	// authenticator's handshake store).
	conn2, res := rawHandshake(t, addr, baseAuthReq(id, secret, protocol, caps))
	if !res.Allowed {
		t.Fatalf("handshake failed: %s", res.Reason)
	}
	membership.Set(id, MembershipStatus{State: MembershipRevoked, Capabilities: caps, Protocol: protocol})
	conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf2 := make([]byte, 1)
	start = time.Now()
	if _, err := conn2.Read(buf2); err == nil {
		t.Fatal("expected connection to terminate after revocation")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("active connection was not terminated promptly after revocation: %v", elapsed)
	}
	_ = conn2.Close()
}

func TestNetTransportRejectsUnknownNode(t *testing.T) {
	protocol := Version
	auth := NewMemoryPeerAuthenticator(protocol)
	membership := NewMemoryMembership()
	nt, err := NewNetTransport(NetTransportOptions{
		ID: "n1", Address: "127.0.0.1:0", Authenticator: auth, Membership: membership,
		Secret: "s", Protocol: protocol, Capabilities: Capabilities{ID: "n1"}, RevalidateEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nt.Close()
	_, res := rawHandshake(t, nt.LocalAddr(), baseAuthReq("ghost", "s", protocol, Capabilities{ID: "ghost"}))
	if res.Allowed {
		t.Fatal("unknown node must be rejected")
	}
	_ = fmt.Sprintf
}
