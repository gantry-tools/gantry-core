package propagation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ConflictPolicy string

const (
	ConflictReject          ConflictPolicy = "reject"
	ConflictSourceWins      ConflictPolicy = "source-wins"
	ConflictDestinationWins ConflictPolicy = "destination-wins"
	ConflictManual          ConflictPolicy = "manual"
	ConflictAuthoritative   ConflictPolicy = "authoritative-source"
)

type SecretRef struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}
type Actor struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Permission string `json:"permission"`
}
type Envelope struct {
	Kind          string          `json:"kind"`
	ID            string          `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	Revision      int64           `json:"revision"`
	SourceNode    string          `json:"source_node"`
	CreatedAt     time.Time       `json:"created_at"`
	Digest        string          `json:"digest"`
	Dependencies  []string        `json:"dependencies,omitempty"`
	Secrets       []SecretRef     `json:"secret_refs,omitempty"`
	Target        string          `json:"target"`
	Conflict      ConflictPolicy  `json:"conflict_policy"`
	Actor         Actor           `json:"actor"`
	Payload       json.RawMessage `json:"payload"`
	Signature     string          `json:"signature,omitempty"`
}

func CanonicalDigest(payload json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (e *Envelope) Validate() error {
	if strings.TrimSpace(e.Kind) == "" || strings.TrimSpace(e.ID) == "" {
		return errors.New("kind and id are required")
	}
	if e.SchemaVersion < 1 || e.Revision < 1 {
		return errors.New("schema_version and revision must be positive")
	}
	if strings.TrimSpace(e.SourceNode) == "" || strings.TrimSpace(e.Target) == "" {
		return errors.New("source_node and target are required")
	}
	switch e.Conflict {
	case ConflictReject, ConflictSourceWins, ConflictDestinationWins, ConflictManual, ConflictAuthoritative:
	default:
		return fmt.Errorf("unsupported conflict policy %q", e.Conflict)
	}
	if len(e.Payload) == 0 {
		return errors.New("payload is required")
	}
	d, err := CanonicalDigest(e.Payload)
	if err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	if e.Digest == "" {
		e.Digest = d
	} else if e.Digest != d {
		return errors.New("digest does not match payload")
	}
	seen := map[string]bool{}
	for _, s := range e.Secrets {
		if s.Name == "" {
			return errors.New("secret reference name is required")
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate secret reference %q", s.Name)
		}
		seen[s.Name] = true
	}
	sort.Strings(e.Dependencies)
	return nil
}
