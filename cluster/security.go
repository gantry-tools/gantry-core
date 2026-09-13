package cluster

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const MaxRequestBytes int64 = 256 << 10
const ClockSkew = 5 * time.Minute

const (
	HeaderNode       = "X-Gantry-Node"
	HeaderTimestamp  = "X-Gantry-Timestamp"
	HeaderNonce      = "X-Gantry-Nonce"
	HeaderRequestID  = "X-Gantry-Request-ID"
	HeaderSignature  = "X-Gantry-Signature"
	HeaderProtocol   = "X-Gantry-Protocol"
	HeaderCapability = "X-Gantry-Capability"
)

type RequestEnvelope struct {
	NodeID     string
	Timestamp  time.Time
	Nonce      string
	RequestID  string
	Protocol   int
	Capability string
	Signature  string
}

func Sign(secret, method, requestURI, timestamp, nonce, requestID, capability string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s\n%s\n%s\n%x", method, requestURI, timestamp, nonce, requestID, capability, bodyHash)
	return mac.Sum(nil)
}

func Signature(secret, method, requestURI, timestamp, nonce, requestID, capability string, body []byte) string {
	return base64.RawURLEncoding.EncodeToString(Sign(secret, method, requestURI, timestamp, nonce, requestID, capability, body))
}

func VerifySignature(secret, signature, method, requestURI, timestamp, nonce, requestID, capability string, body []byte) bool {
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(got, Sign(secret, method, requestURI, timestamp, nonce, requestID, capability, body))
}

func ReadEnvelope(h http.Header, now time.Time) (RequestEnvelope, error) {
	protocol, err := strconv.Atoi(h.Get(HeaderProtocol))
	if err != nil {
		return RequestEnvelope{}, errors.New("incompatible cluster protocol")
	}
	at, err := time.Parse(time.RFC3339Nano, h.Get(HeaderTimestamp))
	if err != nil {
		return RequestEnvelope{}, errors.New("invalid cluster timestamp")
	}
	e := RequestEnvelope{NodeID: h.Get(HeaderNode), Timestamp: at, Nonce: h.Get(HeaderNonce), RequestID: h.Get(HeaderRequestID), Protocol: protocol, Capability: h.Get(HeaderCapability), Signature: h.Get(HeaderSignature)}
	if e.NodeID == "" || e.Nonce == "" || e.RequestID == "" || e.Signature == "" {
		return RequestEnvelope{}, errors.New("cluster authentication required")
	}
	if at.Before(now.Add(-ClockSkew)) || at.After(now.Add(ClockSkew)) {
		return RequestEnvelope{}, errors.New("cluster request outside clock-skew window")
	}
	return e, nil
}

func WriteEnvelope(h http.Header, nodeID, secret, method, requestURI, capability string, protocol int, body []byte, now time.Time, nonce, requestID string) {
	timestamp := now.UTC().Format(time.RFC3339Nano)
	h.Set(HeaderNode, nodeID)
	h.Set(HeaderTimestamp, timestamp)
	h.Set(HeaderNonce, nonce)
	h.Set(HeaderRequestID, requestID)
	h.Set(HeaderProtocol, strconv.Itoa(protocol))
	h.Set(HeaderCapability, capability)
	h.Set(HeaderSignature, Signature(secret, method, requestURI, timestamp, nonce, requestID, capability, body))
}
