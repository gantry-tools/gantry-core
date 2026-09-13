package propagation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretsRemainReferences(t *testing.T) {
	e := Envelope{Kind: "alert-policy", ID: "a", SchemaVersion: 1, Revision: 1, SourceNode: "n", Target: "all", Conflict: ConflictReject, Payload: json.RawMessage(`{"destination":"secret://pager"}`), Secrets: []SecretRef{{Name: "pager", Required: true}}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "super-secret-value") {
		t.Fatal("raw secret leaked")
	}
	if _, err := CheckSecrets(e.Secrets, MapSecrets{"pager": false}); err == nil {
		t.Fatal("missing required secret accepted")
	}
}
