package terminal

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompatibility(t *testing.T) {
	var cases []struct {
		Name                 string
		Input                Session
		WantTitle, WantState string
	}
	b, readErr := os.ReadFile("testdata/compatibility.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if e := json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		got, e := Normalize(c.Input)
		if e != nil || got.Title != c.WantTitle || got.State != c.WantState {
			t.Errorf("%s: %#v %v", c.Name, got, e)
		}
	}
}
func TestScrollback(t *testing.T) {
	got := AppendScrollback("abc", []byte{0xff, 'd', 'e', 'f'}, 8)
	if !utf8.ValidString(got) || len([]byte(got)) > 8 || !strings.HasSuffix(got, "def") {
		t.Fatalf("bad scrollback %q", got)
	}
}
func TestCapacity(t *testing.T) {
	if CheckCapacity(false, 16, 16) == nil {
		t.Fatal("accepted over-capacity session")
	}
	if e := CheckCapacity(true, 16, 16); e != nil {
		t.Fatal(e)
	}
}
