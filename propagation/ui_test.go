package propagation

import "testing"

func TestPresentResultKeepsPartialFailuresVisible(t *testing.T) {
	p := PresentResult(TransactionResult{Results: []NodeResult{{Node: "a", Applied: true}, {Node: "b", Error: "partition"}, {Node: "c", Applied: true, RolledBack: true}}})
	if p[0].State != "applied" || p[1].State != "failed" || p[2].State != "rolled-back" {
		t.Fatalf("%+v", p)
	}
}
