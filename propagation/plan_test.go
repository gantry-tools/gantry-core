package propagation

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDiffPreviewAndSignedPlan(t *testing.T) {
	src := []Envelope{{Kind: "monitor", ID: "new", SchemaVersion: 1, Revision: 1, SourceNode: "n1", Target: "all", Conflict: ConflictReject, Payload: json.RawMessage(`{"x":1}`)}}
	items, err := Diff(src, map[string]Existing{"monitor/old": {Kind: "monitor", ID: "old", Revision: 3, Digest: "old"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Change != ChangeCreate || items[1].Change != ChangeDelete {
		t.Fatalf("unexpected preview: %+v", items)
	}
	p := NewPlan("p1", "n1", "all", items, time.Unix(1, 0))
	key := []byte("1234567890123456")
	if err := p.Sign(key); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(key); err != nil {
		t.Fatal(err)
	}
	p.Target = "node:tampered"
	if err := p.Verify(key); err == nil {
		t.Fatal("tampered plan verified")
	}
}
