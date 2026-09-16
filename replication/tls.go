package replication

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

// LoadTLSConfig builds the Gantry replication transport TLS config from the
// cert/key/CA triplet (mutual TLS contract: all three are required). Peer
// identity is bound by the mutual Gantry handshake over the encrypted channel -
// certificate verification provides channel trust, never identity - and the
// server additionally requires and verifies peer certificates against the
// configured CA.
//
// InsecureSkipVerify is present on the shared client+server config because the
// NetTransport dials arbitrary peers with one config (no per-dial ServerName).
// The remote endpoint is authenticated by the mutual Gantry handshake (pairwise
// credential digest + signed handshake + nonce replay), so skipping TLS
// hostname verification does not weaken endpoint identity; it only leaves
// channel confidentiality/integrity to the TLS session.
func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" && caFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, errors.New("replication TLS requires the full cert/key/CA triplet")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("no certificates found in replication tls_ca")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert}, ClientCAs: pool, RootCAs: pool,
		ClientAuth: tls.RequireAndVerifyClientCert, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12,
	}, nil
}

// LeaderProposeHandler is the authenticated leader-side proposal endpoint for
// the Gantry application RPC. authenticate verifies the inbound peer request
// (the live wiring uses the product's authenticated peer transport); route
// realizes the forwarded mutation intent through the leader's own product
// adapter. The handler mechanics (authentication, envelope decode, result
// encoding, error mapping) are generic; the semantic payload and its routing
// are the product's.
func LeaderProposeHandler(authenticate func(*http.Request) (string, error), route func(context.Context, ForwardRequest) (*ApplyResult, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := authenticate(r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var fr ForwardRequest
		if err := json.Unmarshal(body, &fr); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		res, err := route(r.Context(), fr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
}
