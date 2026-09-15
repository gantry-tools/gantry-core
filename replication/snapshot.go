package replication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// SnapshotFormatVersion is the semantic snapshot envelope version: what a
// snapshot means. It is distinct from the Raft snapshot metadata (index/term)
// and from any product physical storage schema.
const SnapshotFormatVersion = 1

// SnapshotEnvelope is the versioned semantic snapshot. It carries replicated
// state only - node-local identity, credentials, pairing, nonces, sessions,
// telemetry and scheduler execution state never appear in it.
type SnapshotEnvelope struct {
	FormatVersion      int                  `json:"format_version"`
	ReplicationVersion int                  `json:"replication_version"` // semantic schema the state was produced under
	Product            string               `json:"product,omitempty"`
	RaftIndex          uint64               `json:"raft_index"` // committed raft index the state represents
	RaftTerm           uint64               `json:"raft_term"`
	State              map[string]kvEntry   `json:"state"`
	AppliedOps         map[string]appliedOp `json:"applied_ops"` // durable op-ID/idempotency required after compaction
	Integrity          string               `json:"integrity"`   // sha256 over the canonical content (Integrity excluded)
}

// Encode returns the canonical snapshot bytes with an integrity digest.
// Equal semantic state always encodes to equal bytes (map keys are sorted by
// encoding/json).
func (e *SnapshotEnvelope) Encode() ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	d, err := e.contentDigest()
	if err != nil {
		return nil, err
	}
	e.Integrity = d
	return json.Marshal(e)
}

// DecodeSnapshot parses and integrity-verifies a canonical snapshot. Semantic
// compatibility (format/replication version) is enforced separately by the
// receiving FSM before installation.
func DecodeSnapshot(b []byte) (*SnapshotEnvelope, error) {
	var e SnapshotEnvelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	d, err := e.contentDigest()
	if err != nil {
		return nil, err
	}
	if e.Integrity != d {
		return nil, errors.New("snapshot integrity mismatch")
	}
	return &e, nil
}

func (e *SnapshotEnvelope) validate() error {
	if e.FormatVersion != SnapshotFormatVersion {
		return fmt.Errorf("unsupported snapshot format version %d", e.FormatVersion)
	}
	if !SupportedVersion(e.ReplicationVersion) {
		return fmt.Errorf("unsupported snapshot replication version %d", e.ReplicationVersion)
	}
	return nil
}

// contentDigest hashes the canonical JSON of the envelope with Integrity
// cleared, so the digest covers every semantic field.
func (e *SnapshotEnvelope) contentDigest() (string, error) {
	cp := *e
	cp.Integrity = ""
	b, err := json.Marshal(cp)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
