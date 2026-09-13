package propagation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Change string

const (
	ChangeCreate   Change = "create"
	ChangeUpdate   Change = "update"
	ChangeDelete   Change = "delete"
	ChangeNoop     Change = "noop"
	ChangeConflict Change = "conflict"
)

type Existing struct {
	Kind, ID, Digest, Owner string
	Revision                int64
	SchemaVersion           int
}
type PlanItem struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Change       Change `json:"change"`
	Reason       string `json:"reason,omitempty"`
	FromRevision int64  `json:"from_revision,omitempty"`
	ToRevision   int64  `json:"to_revision,omitempty"`
}
type Plan struct {
	ID         string     `json:"id"`
	SourceNode string     `json:"source_node"`
	Target     string     `json:"target"`
	CreatedAt  time.Time  `json:"created_at"`
	Items      []PlanItem `json:"items"`
	Signature  string     `json:"signature,omitempty"`
}

func Diff(source []Envelope, dest map[string]Existing, allowDelete bool) ([]PlanItem, error) {
	out := make([]PlanItem, 0, len(source))
	seen := map[string]bool{}
	for i := range source {
		e := source[i]
		if err := e.Validate(); err != nil {
			return nil, err
		}
		key := e.Kind + "/" + e.ID
		seen[key] = true
		d, ok := dest[key]
		if !ok {
			out = append(out, PlanItem{Kind: e.Kind, ID: e.ID, Change: ChangeCreate, ToRevision: e.Revision})
			continue
		}
		if d.Digest == e.Digest {
			out = append(out, PlanItem{Kind: e.Kind, ID: e.ID, Change: ChangeNoop, FromRevision: d.Revision, ToRevision: e.Revision})
			continue
		}
		ch, reason := resolveConflict(e, d)
		out = append(out, PlanItem{Kind: e.Kind, ID: e.ID, Change: ch, Reason: reason, FromRevision: d.Revision, ToRevision: e.Revision})
	}
	if allowDelete {
		for k, d := range dest {
			if seen[k] {
				continue
			}
			p := strings.SplitN(k, "/", 2)
			out = append(out, PlanItem{Kind: p[0], ID: p[1], Change: ChangeDelete, FromRevision: d.Revision})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].ID < out[j].ID
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}
func resolveConflict(e Envelope, d Existing) (Change, string) {
	if e.Revision <= d.Revision {
		switch e.Conflict {
		case ConflictSourceWins:
			return ChangeUpdate, "source wins despite destination revision"
		case ConflictDestinationWins:
			return ChangeNoop, "destination wins"
		case ConflictAuthoritative:
			if d.Owner == "" || d.Owner == e.SourceNode {
				return ChangeUpdate, "authoritative source"
			}
			return ChangeConflict, "different authoritative owner"
		case ConflictMerge:
			return ChangeConflict, "merge requires an object-specific merge adapter"
		case ConflictManual:
			return ChangeConflict, "manual approval required"
		default:
			return ChangeConflict, "destination is same or newer revision"
		}
	}
	if e.Conflict == ConflictDestinationWins {
		return ChangeNoop, "destination wins"
	}
	return ChangeUpdate, "newer source revision"
}
func NewPlan(id, source, target string, items []PlanItem, now time.Time) Plan {
	return Plan{ID: id, SourceNode: source, Target: target, CreatedAt: now.UTC(), Items: items}
}
func (p Plan) canonical() ([]byte, error) { cp := p; cp.Signature = ""; return json.Marshal(cp) }
func (p *Plan) Sign(key []byte) error {
	if len(key) < 16 {
		return errors.New("plan signing key too short")
	}
	b, err := p.canonical()
	if err != nil {
		return err
	}
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(b)
	p.Signature = hex.EncodeToString(m.Sum(nil))
	return nil
}
func (p Plan) Verify(key []byte) error {
	want := p.Signature
	if want == "" {
		return errors.New("missing plan signature")
	}
	b, err := p.canonical()
	if err != nil {
		return err
	}
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(b)
	got, err := hex.DecodeString(want)
	if err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	if !hmac.Equal(got, m.Sum(nil)) {
		return errors.New("invalid plan signature")
	}
	return nil
}
