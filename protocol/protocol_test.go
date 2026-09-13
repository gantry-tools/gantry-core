package protocol

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gantry-tools/gantry-core/cli"
)

func TestDecodeStrictAndBounded(t *testing.T) {
	var value struct {
		Name string `json:"name"`
	}
	if err := DecodeStrict(strings.NewReader(`{"name":"one"}`), 100, &value); err != nil || value.Name != "one" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
	for _, input := range []string{`{"name":"one","extra":true}`, `{"name":"one"} {}`, `{"name":"a very long value"}`} {
		if err := DecodeStrict(strings.NewReader(input), 20, &value); err == nil {
			t.Fatalf("input %q unexpectedly accepted", input)
		}
	}
}

func TestErrorMappings(t *testing.T) {
	err := &Error{Code: Conflict, Message: "already exists"}
	if err.HTTPStatus() != 409 || ExitCode(err) != cli.ExitConflict {
		t.Fatalf("status=%d exit=%d", err.HTTPStatus(), ExitCode(err))
	}
	if ExitCode(errors.New("plain")) != cli.ExitFailure {
		t.Fatal("ordinary error must map to generic failure")
	}
}

func TestRedactMakesDeepCopy(t *testing.T) {
	original := map[string]any{"token": "secret", "nested": map[string]any{"password": "hidden"}, "items": []any{map[string]any{"key": "value"}}}
	redacted, err := Redact(original, []string{"/token", "/nested/password", "/items/0/key"})
	if err != nil {
		t.Fatal(err)
	}
	got := redacted.(map[string]any)
	if got["token"] != "[redacted]" || original["token"] != "secret" {
		t.Fatalf("redacted=%#v original=%#v", got, original)
	}
}

func TestRenderModes(t *testing.T) {
	table := Table{Columns: []string{"NAME", "STATE"}, Rows: [][]string{{"node-a", "ready"}}}
	var plain bytes.Buffer
	if err := Render(&plain, cli.Plain, table); err != nil || plain.String() != "node-a\tready\n" {
		t.Fatalf("plain=%q err=%v", plain.String(), err)
	}
	var jsonOut bytes.Buffer
	if err := Render(&jsonOut, cli.JSON, table); err != nil || !strings.Contains(jsonOut.String(), `"columns"`) {
		t.Fatalf("json=%q err=%v", jsonOut.String(), err)
	}
}

func TestPageKeyAndConfirmation(t *testing.T) {
	if got := NormalizePage(0, 1000, 200); !reflect.DeepEqual(got, Page{Number: 1, Size: 200}) {
		t.Fatalf("page=%#v", got)
	}
	if !ValidKey("request-1234") || ValidKey("short") || ValidKey("request key") {
		t.Fatal("key validation mismatch")
	}
	if err := Confirm("DELETE cortex", "DELETE Cortex"); err == nil {
		t.Fatal("confirmation must match exactly")
	}
	first, err := NewRequestID()
	if err != nil || len(first) != 32 {
		t.Fatalf("request id=%q err=%v", first, err)
	}
}
