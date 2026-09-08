package agent

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCompatibility(t *testing.T) {
	var cases []struct {
		Name        string     `json:"name"`
		Snapshot    Snapshot   `json:"snapshot"`
		StdoutError bool       `json:"stdoutError"`
		ValidStop   bool       `json:"validStop"`
		Exit        ExitStatus `json:"exit"`
		Want        Outcome    `json:"want"`
	}
	b, e := os.ReadFile("testdata/compatibility.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := Classify(c.Snapshot, c.StdoutError, c.ValidStop, c.Exit, CauseNone); got != c.Want {
				t.Fatalf("got %q want %q", got, c.Want)
			}
		})
	}
}
func TestCausePrecedenceAndSeal(t *testing.T) {
	s := NewRunState()
	if !s.RecordCause(CauseRequestCanceled) || !s.RecordCause(CauseUserStop) {
		t.Fatal("user stop must upgrade request cancellation")
	}
	s.Seal()
	if s.RecordCause(CauseOutputLimit) {
		t.Fatal("sealed state accepted transition")
	}
}
func TestRecovery(t *testing.T) {
	text, suppress, replace := ReconcileRecovered("hello", "hello world")
	if text != "world" || suppress || replace {
		t.Fatalf("unexpected %q %v %v", text, suppress, replace)
	}
}

func TestRecoveryCompatibility(t *testing.T) {
	tests := []struct {
		name                 string
		streamed, recovered  string
		want                 string
		suppressed, replaced bool
	}{
		{"empty", "hello", "", "", true, false},
		{"duplicate", "hello", "hello", "", true, false},
		{"suffix", "hello", "hello world", "world", false, false},
		{"overlap", "hello wor", "world", "ld", false, false},
		{"replace", "world", "hello world", "hello world", false, true},
		{"unrelated", "hello", "world", "world", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, suppressed, replaced := ReconcileRecovered(tt.streamed, tt.recovered)
			if got != tt.want || suppressed != tt.suppressed || replaced != tt.replaced {
				t.Fatalf("got (%q, %v, %v), want (%q, %v, %v)", got, suppressed, replaced, tt.want, tt.suppressed, tt.replaced)
			}
		})
	}
}

func TestRunArgsPreservesOptionBoundary(t *testing.T) {
	want := []string{"--print-logs", "--log-level", "WARN", "run", "--format", "json", "--auto", "--dir", "/work", "--model", "provider/model", "--session", "session-1", "--file", "a.txt", "--file", "b.txt", "--", "explain --file literally"}
	got := RunArgs("/work", "provider/model", " session-1 ", []string{"a.txt", "b.txt"}, "explain --file literally")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
