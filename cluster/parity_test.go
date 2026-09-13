package cluster

import "testing"

func TestPhase4CommonContract(t *testing.T) {
	wantCLI := []string{"cluster init", "cluster invite", "cluster join", "cluster approve", "cluster members", "cluster status", "cluster rotate", "cluster revoke", "cluster remove"}
	seen := map[string]bool{}
	for _, c := range CommonCLI {
		seen[c.Path] = true
	}
	for _, want := range wantCLI {
		if !seen[want] {
			t.Fatalf("missing common CLI command %q", want)
		}
	}
	if JoinPath != "/api/cluster/v1/join" || RPCSummaryPath == "" {
		t.Fatal("unstable common paths")
	}
	if len(CommonLifecycle) < 8 || len(CommonManagementSections) < 7 {
		t.Fatal("incomplete phase 4 contract")
	}
	for _, state := range []string{MemberActive, MemberDisabled, MemberRevoked} {
		found := false
		for _, got := range CommonMemberStates {
			found = found || got == state
		}
		if !found {
			t.Fatalf("missing state %s", state)
		}
	}
}
