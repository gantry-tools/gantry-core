package propagation

import (
	"encoding/json"
	"testing"
)

func TestConflictPolicies(t *testing.T) {
	base := Envelope{Kind: "monitor", ID: "m", SchemaVersion: 1, Revision: 2, SourceNode: "source", Target: "all", Payload: json.RawMessage(`{"x":2}`)}
	dest := Existing{Kind: "monitor", ID: "m", Revision: 3, Digest: "old", Owner: "other"}
	cases := []struct {
		policy ConflictPolicy
		want   Change
	}{
		{ConflictReject, ChangeConflict}, {ConflictSourceWins, ChangeUpdate}, {ConflictDestinationWins, ChangeNoop}, {ConflictManual, ChangeConflict}, {ConflictAuthoritative, ChangeConflict},
	}
	for _, tc := range cases {
		e := base
		e.Conflict = tc.policy
		got, _ := ResolveConflict(e, dest)
		if got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.policy, got, tc.want)
		}
	}
	if err := ValidateOwnership(ConflictAuthoritative, Ownership{ObjectOwner: "other", SourceNode: "source"}); err == nil {
		t.Fatal("expected ownership rejection")
	}
}
