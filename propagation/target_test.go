package propagation

import "testing"

func TestTargetSelection(t *testing.T) {
	members := []Member{
		{ID: "a", Enabled: true, Labels: []string{"prod"}, Capabilities: []string{"propagate", "monitor"}},
		{ID: "b", Enabled: true, Labels: []string{"prod"}, Capabilities: []string{"propagate"}},
		{ID: "c", Enabled: false, Labels: []string{"prod"}, Capabilities: []string{"propagate", "monitor"}},
	}
	got, err := Select(Selector{Labels: []string{"prod"}, Capabilities: []string{"monitor"}, Exclude: []string{"b"}}, members)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
}
