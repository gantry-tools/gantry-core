package replication

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/hashicorp/raft"
)

func TestBoltStoreRoundTripAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft.db")
	s, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}

	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("one")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("two")},
		{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("three")},
	}
	if err := s.StoreLogs(logs); err != nil {
		t.Fatal(err)
	}
	if fi, err := s.FirstIndex(); err != nil || fi != 1 {
		t.Fatalf("first index: %d %v", fi, err)
	}
	if li, err := s.LastIndex(); err != nil || li != 3 {
		t.Fatalf("last index: %d %v", li, err)
	}
	var got raft.Log
	if err := s.GetLog(2, &got); err != nil {
		t.Fatal(err)
	}
	if got.Index != 2 || got.Term != 1 || !bytes.Equal(got.Data, []byte("two")) {
		t.Fatalf("log mismatch: %+v", got)
	}

	if err := s.SetUint64([]byte("currentTerm"), 7); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("votedFor"), []byte("n2")); err != nil {
		t.Fatal(err)
	}
	term, err := s.GetUint64([]byte("currentTerm"))
	if err != nil || term != 7 {
		t.Fatalf("stable term: %d %v", term, err)
	}

	if err := s.DeleteRange(2, 3); err != nil {
		t.Fatal(err)
	}
	if li, _ := s.LastIndex(); li != 1 {
		t.Fatalf("expected last index 1 after delete range, got %d", li)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: durable state must survive.
	s2, err := NewBoltStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if li, _ := s2.LastIndex(); li != 1 {
		t.Fatalf("reopen last index: %d", li)
	}
	if err := s2.GetLog(1, &got); err != nil {
		t.Fatal(err)
	}
	if term, _ := s2.GetUint64([]byte("currentTerm")); term != 7 {
		t.Fatalf("reopen stable term: %d", term)
	}
	if v, _ := s2.Get([]byte("votedFor")); string(v) != "n2" {
		t.Fatalf("reopen stable votedFor: %q", v)
	}
}
