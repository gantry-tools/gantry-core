package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCompatibility(t *testing.T) {
	var cases []struct {
		Name, Content, Replacement, Want string
		Query                            Query
		Count                            int
	}
	b, readErr := os.ReadFile("testdata/compatibility.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if e := json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, n, e := Replace([]byte(c.Content), c.Query, c.Replacement, 100)
			if e != nil || string(got) != c.Want || n != c.Count {
				t.Fatalf("got %q/%d/%v want %q/%d", got, n, e, c.Want, c.Count)
			}
		})
	}
}
func TestAtomicWritePreservesModeAndRejectsStaleRevision(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	if e := os.WriteFile(p, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	rev, err := FileRevision(p)
	if err != nil {
		t.Fatal(err)
	}
	if e := WriteAtomic(p, []byte("new"), &rev); e != nil {
		t.Fatal(e)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	if e := WriteAtomic(p, []byte("bad"), &rev); e == nil {
		t.Fatal("accepted stale revision")
	}
}
func TestBinaryRejected(t *testing.T) {
	if _, _, e := Replace([]byte{'a', 0, 'b'}, Query{Pattern: "a"}, "x", 1); e == nil {
		t.Fatal("accepted binary input")
	}
}
