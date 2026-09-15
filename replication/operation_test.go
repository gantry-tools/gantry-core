package replication

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationEncodeDecodeRoundTrip(t *testing.T) {
	op := Operation{
		ID:         "op-1",
		Product:    "watchpost",
		Version:    Version,
		Kind:       KindSet,
		ObjectKind: "key",
		ObjectID:   "foo",
		Revision:   0,
		Payload:    json.RawMessage(`{"value":"A"}`),
		OriginNode: "n1",
		Actor:      "admin",
	}
	b, err := op.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeOperation(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != op.ID || got.Kind != op.Kind || got.ObjectID != op.ObjectID || got.Product != op.Product {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Committed.Timestamp == "" {
		t.Fatal("committed timestamp not embedded")
	}
}

func TestOperationDeterministicEncoding(t *testing.T) {
	op := Operation{
		ID: "op-1", Product: "watchpost", Version: Version, Kind: KindSet,
		ObjectKind: "key", ObjectID: "foo", Payload: json.RawMessage(`{"value":"A","b":1}`),
		OriginNode: "n1",
	}
	b1, err := op.Encode()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := op.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("operation encoding is not deterministic")
	}
	// canonicalization: reordered JSON payload must produce identical bytes
	op.Payload = json.RawMessage(`{"b":1,"value":"A"}`)
	b3, err := op.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b3) {
		t.Fatal("payload canonicalization failed")
	}
}

func TestOperationValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Operation)
		wantErr string
	}{
		{"empty id", func(o *Operation) { o.ID = "" }, "id is required"},
		{"empty product", func(o *Operation) { o.Product = "" }, "namespace is required"},
		{"bad version", func(o *Operation) { o.Version = Version + 1 }, "unsupported replication operation version"},
		{"zero version", func(o *Operation) { o.Version = 0 }, "unsupported replication operation version"},
		{"empty kind", func(o *Operation) { o.Kind = "" }, "operation kind is required"},
		{"empty object id", func(o *Operation) { o.ObjectID = "" }, "object identity is required"},
		{"empty payload", func(o *Operation) { o.Payload = nil }, "payload is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := Operation{
				ID: "op", Product: "watchpost", Version: Version, Kind: KindSet,
				ObjectKind: "key", ObjectID: "foo", Payload: json.RawMessage(`{"value":"A"}`),
			}
			tc.mutate(&op)
			_, err := op.Encode()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestOperationDigest(t *testing.T) {
	a := Operation{Payload: json.RawMessage(`{"value":"A"}`)}
	b := Operation{Payload: json.RawMessage(`{"value":"B"}`)}
	c := Operation{Payload: json.RawMessage(`{"value":"A"}`)}
	da, _ := a.Digest()
	db, _ := b.Digest()
	dc, _ := c.Digest()
	if da == db {
		t.Fatal("distinct payloads produced the same digest")
	}
	if da != dc {
		t.Fatal("identical payloads produced different digests")
	}
}
