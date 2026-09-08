package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCompatibility(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "existing"), 0700); e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	r, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Path   string
		Status Status
	}
	b, readErr := os.ReadFile("testdata/compatibility.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		if got := r.Status(c.Path); got != c.Status {
			t.Errorf("%q: got %q want %q", c.Path, got, c.Status)
		}
	}
	if _, e = r.Resolve("escape"); !errorsIsEscape(e) {
		t.Fatalf("escape resolve: %v", e)
	}
	if p, e := r.ResolveForCreate("new"); e != nil || p != filepath.Join(root, "new") {
		t.Fatalf("create %q %v", p, e)
	}
}
func errorsIsEscape(e error) bool { return errors.Is(e, ErrEscape) }
