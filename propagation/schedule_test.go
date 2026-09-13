package propagation

import (
	"testing"
	"time"
)

func TestDueAndMaintenanceWindow(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	p := Profile{ID: "x", Name: "x", Kinds: []string{"k"}, Mode: ReconcileAutomatic, Enabled: true, Schedule: "1h", MaintenanceWindow: "23:00-02:00"}
	ok, e := Due(p, now)
	if e != nil || !ok {
		t.Fatalf("due=%v err=%v", ok, e)
	}
	last := now.Add(-30 * time.Minute)
	p.LastRunAt = &last
	ok, e = Due(p, now)
	if e != nil || ok {
		t.Fatalf("unexpected due=%v err=%v", ok, e)
	}
	p.MaintenanceWindow = "02:00-03:00"
	p.LastRunAt = nil
	ok, _ = Due(p, now)
	if ok {
		t.Fatal("outside maintenance window")
	}
}
