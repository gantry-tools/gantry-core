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

func TestGeneratedIdentityAndInvitation(t *testing.T) {
	now := time.Now().UTC()
	generated, err := GenerateIdentity("n_", "i_", []string{"b", "a", "a"}, ProtocolVersion, "dev", now)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Identity.NodeID == "" || generated.Identity.InstallationID == "" || len(generated.PrivateKey) == 0 {
		t.Fatal("generated identity incomplete")
	}
	if len(generated.Identity.Capabilities) != 2 || generated.Identity.Capabilities[0] != "a" {
		t.Fatalf("capabilities=%v", generated.Identity.Capabilities)
	}
	inv, token, err := NewInvitation(now)
	if err != nil {
		t.Fatal(err)
	}
	if inv.State != PairingPending || token == "" || !inv.ExpiresAt.After(now) {
		t.Fatal("invalid generated invitation")
	}
	if !VerifySecretDigest(token, SecretDigest(token)) {
		t.Fatal("secret digest mismatch")
	}
}

func TestVerifyIncomingPromotesPending(t *testing.T) {
	now := time.Now().UTC()
	secret := "01234567890123456789012345678901"
	pending := "abcdefghijklmnopqrstuvwxyzABCDEF"
	body := []byte(`{"ok":true}`)
	timestamp := now.Format(time.RFC3339Nano)
	env := RequestEnvelope{NodeID: "peer", Timestamp: now, Nonce: "nonce", RequestID: "req", Protocol: ProtocolVersion, Capability: "cluster.health"}
	env.Signature = Signature(pending, http.MethodPost, "/rpc", timestamp, env.Nonce, env.RequestID, env.Capability, body)
	expires := now.Add(time.Minute)
	result, err := VerifyIncoming(VerifyRequestInput{Material: AuthMaterial{State: MemberActive, Protocol: ProtocolVersion, Capabilities: []string{"cluster.health"}, CurrentHash: SecretDigest(secret), PendingHash: SecretDigest(pending), PendingExpires: &expires}, RequiredCapability: "cluster.health", PresentedSecret: pending, Method: http.MethodPost, RequestURI: "/rpc", Envelope: env, Body: body, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PromotePending {
		t.Fatal("pending credential was not promoted")
	}
}
