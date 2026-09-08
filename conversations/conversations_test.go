package conversations

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCompatibility(t *testing.T) {
	var f struct{ Server, Client, Want []Event }
	b, e := os.ReadFile("testdata/compatibility.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	if got := MergeEvents(f.Server, f.Client); !reflect.DeepEqual(got, f.Want) {
		t.Fatalf("got %#v want %#v", got, f.Want)
	}
}
func TestValidation(t *testing.T) {
	if e := Validate(Record{ID: "valid_1", Events: []Event{{Kind: "user"}}}); e != nil {
		t.Fatal(e)
	}
	if Validate(Record{ID: "../bad"}) == nil {
		t.Fatal("accepted invalid id")
	}
	if Validate(Record{ID: "ok", Workspace: "bad\npath"}) == nil {
		t.Fatal("accepted control character")
	}
}
