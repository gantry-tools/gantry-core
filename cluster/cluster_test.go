package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"
)

func testIdentity() Identity {
	return Identity{NodeID: "node-a", InstallationID: "install-a", PublicEndpoint: "https://a.test", PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ProtocolVersion: ProtocolVersion, Capabilities: []string{"cluster.health"}}
}

func TestIdentityFingerprintAndCompatibility(t *testing.T) {
	i := testIdentity()
	if i.Fingerprint() == "" {
		t.Fatal("empty fingerprint")
	}
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Compatible(1, 1, i.Capabilities, i.Capabilities, "cluster.health"); err != nil {
		t.Fatal(err)
	}
	if err := Compatible(1, 2, nil, nil, ""); err == nil {
		t.Fatal("expected incompatibility")
	}
}
func TestPairingValidation(t *testing.T) {
	now := time.Now()
	inv := Invitation{State: PairingPending, ExpiresAt: now.Add(time.Minute)}
	if err := ValidateInvitation(inv, now); err != nil {
		t.Fatal(err)
	}
	in := JoinSubmission{InvitationToken: "token", Identity: testIdentity(), CredentialForHost: string(make([]byte, MinimumCredentialBytes))}
	if err := ValidateJoinSubmission(in, now); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(PairingPending, PairingApproved); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(PairingRejected, PairingApproved); err == nil {
		t.Fatal("invalid transition accepted")
	}
}
func TestEnvelopeRoundTrip(t *testing.T) {
	h := http.Header{}
	now := time.Now().UTC()
	body := []byte("abc")
	WriteEnvelope(h, "node-a", "secret", http.MethodPost, "/rpc", "cluster.health", 1, body, now, "nonce", "req")
	e, err := ReadEnvelope(h, now)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySignature("secret", e.Signature, http.MethodPost, "/rpc", h.Get(HeaderTimestamp), e.Nonce, e.RequestID, e.Capability, body) {
		t.Fatal("signature failed")
	}
}
func TestTargetAndFanout(t *testing.T) {
	target, err := ParseTarget("node:abcd")
	if err != nil || target.NodeID != "abcd" {
		t.Fatal(target, err)
	}
	got := FanOut(context.Background(), []string{"b", "a"}, 1, func(_ context.Context, id string) (string, error) {
		if id == "b" {
			return "", errors.New("down")
		}
		return id, nil
	})
	if len(got) != 2 || got[0].NodeID != "a" || !IsPartial(got) {
		t.Fatalf("%#v", got)
	}
}
