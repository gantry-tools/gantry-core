package replication

import (
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func TestSignVerifyHandshakeExports(t *testing.T) {
	caps := Capabilities{ID: "n1", OperationSchemaVersions: []int{1, Version}, SnapshotFormatVersions: []int{SnapshotFormatVersion}}
	req, err := SignHandshake("secret-1", "n1", "n1", "node-n1", Version, caps, "nonce-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if req.PresentedSecret != "secret-1" {
		t.Fatalf("presented secret not carried: %q", req.PresentedSecret)
	}
	if !VerifyHandshakeSignature("secret-1", *req) {
		t.Fatal("valid signature must verify")
	}
	if VerifyHandshakeSignature("wrong", *req) {
		t.Fatal("wrong secret must not verify")
	}
	// The presented secret is NOT part of the signed body: tampering with it
	// does not change the signature (digest checking is the verifier's job).
	tampered := *req
	tampered.PresentedSecret = "secret-2"
	if !VerifyHandshakeSignature("secret-1", tampered) {
		t.Fatal("presented secret must not be part of the signed canonical body")
	}
	// The signature is over node identity + capabilities + nonce + timestamp.
	bad := *req
	bad.RaftServerID = "n9"
	if VerifyHandshakeSignature("secret-1", bad) {
		t.Fatal("mismatched raft identity must not verify")
	}
	_ = raft.ServerID("x")
}
