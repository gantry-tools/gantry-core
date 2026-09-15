package replication

import (
	"crypto/rand"
	"encoding/base64"
)

// newNonce returns a fresh random nonce for handshakes/requests. It is only
// ever used at proposal/connection time, never during deterministic apply.
func newNonce() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
