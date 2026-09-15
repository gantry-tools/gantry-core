// Package replication provides the generic Gantry replicated-state layer.
//
// Phase 7B implements the frozen Phase 7A decision
// (docs/REPLICATED_STATE_ARCHITECTURE.md): Gantry owns semantic replicated
// operations, deterministic application, idempotency and filtered logical
// snapshots, while an established consensus implementation (hashicorp/raft)
// owns election, terms, the replicated log, quorum commitment and membership
// safety. This package is product-independent; no product tables are
// replicated by it.
package replication

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Version is the replication operation schema version supported by this
// implementation. It is the semantic replication contract version: what
// committed operations and snapshots mean. It is distinct from any product
// storage schema.
const Version = 1

// SupportedVersion reports whether opVersion is supported by this build.
func SupportedVersion(opVersion int) bool { return opVersion >= 1 && opVersion <= Version }

// CommittedMeta carries deterministic metadata resolved at proposal time by
// the leader and applied verbatim by every replica. It must never be derived
// from a receiving node's own clock.
type CommittedMeta struct {
	Timestamp string `json:"timestamp"` // RFC3339Nano, set once by the leader
}

// Operation is the logical replicated operation. The consensus log contains
// canonical encodings of Operation values - never SQL statements, HTTP
// requests, closures or locally allocated row IDs.
type Operation struct {
	ID         string          `json:"id"`          // unique; survives leadership changes
	Product    string          `json:"product"`     // namespace (e.g. "watchpost")
	Version    int             `json:"version"`     // replication operation schema version
	Kind       string          `json:"kind"`        // e.g. "kv.set"
	ObjectKind string          `json:"object_kind"` // e.g. "key"
	ObjectID   string          `json:"object_id"`   // stable explicit identity
	Revision   int64           `json:"revision"`    // expected/precondition revision (0 = none)
	Payload    json.RawMessage `json:"payload"`
	OriginNode string          `json:"origin_node"` // proposing node
	Actor      string          `json:"actor,omitempty"`
	Committed  CommittedMeta   `json:"committed"`
}

// Digest returns the canonical digest of the operation payload.
func (o Operation) Digest() (string, error) {
	p, err := NormalizeJSON(o.Payload)
	if err != nil {
		return "", fmt.Errorf("payload: %w", err)
	}
	sum := sha256.Sum256(p)
	return hex.EncodeToString(sum[:]), nil
}

// NormalizeJSON canonicalizes arbitrary JSON so every replica encodes the
// operation identically.
func NormalizeJSON(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// Encode returns the canonical deterministic encoding of the operation that
// enters the consensus log. The payload is normalized and the committed
// timestamp is embedded before proposal.
func (o *Operation) Encode() ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	p, err := NormalizeJSON(o.Payload)
	if err != nil {
		return nil, err
	}
	o.Payload = p
	if o.Committed.Timestamp == "" {
		o.Committed.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return json.Marshal(o)
}

// DecodeOperation parses an operation from its canonical encoding.
func DecodeOperation(b []byte) (Operation, error) {
	var o Operation
	if err := json.Unmarshal(b, &o); err != nil {
		return Operation{}, err
	}
	if err := o.validate(); err != nil {
		return Operation{}, err
	}
	return o, nil
}

func (o Operation) validate() error {
	if strings.TrimSpace(o.ID) == "" {
		return errors.New("operation id is required")
	}
	if strings.TrimSpace(o.Product) == "" {
		return errors.New("product/namespace is required")
	}
	if !SupportedVersion(o.Version) {
		return fmt.Errorf("unsupported replication operation version %d", o.Version)
	}
	if strings.TrimSpace(o.Kind) == "" {
		return errors.New("operation kind is required")
	}
	if strings.TrimSpace(o.ObjectID) == "" {
		return errors.New("object identity is required")
	}
	if len(bytes.TrimSpace(o.Payload)) == 0 {
		return errors.New("operation payload is required")
	}
	return nil
}

// Known operation kinds for the proving state machine.
const (
	KindSet    = "kv.set"
	KindDelete = "kv.delete"
)

// ValidateKind checks a kind against the proving FSM.
func ValidateKind(kind string) error {
	switch kind {
	case KindSet, KindDelete:
		return nil
	default:
		return fmt.Errorf("unsupported operation kind %q", kind)
	}
}

// sortKeys is a small helper kept deterministic-friendly.
func sortKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
