package propagation

import (
	"fmt"
	"strings"
	"time"
)

// Due reports whether a profile is eligible to run. Schedule is a positive Go
// duration (for example 15m or 6h); an empty schedule is manual-only.
func Due(p Profile, now time.Time) (bool, error) {
	if !p.Enabled || strings.TrimSpace(p.Schedule) == "" {
		return false, nil
	}
	d, err := time.ParseDuration(p.Schedule)
	if err != nil || d <= 0 {
		return false, fmt.Errorf("invalid propagation schedule %q", p.Schedule)
	}
	if p.LastRunAt == nil {
		return InMaintenanceWindow(p.MaintenanceWindow, now)
	}
	if now.Before(p.LastRunAt.Add(d)) {
		return false, nil
	}
	return InMaintenanceWindow(p.MaintenanceWindow, now)
}

// InMaintenanceWindow accepts HH:MM-HH:MM in local clock semantics. An empty
// window permits all times; overnight windows such as 23:00-02:00 are valid.
func InMaintenanceWindow(window string, at time.Time) (bool, error) {
	window = strings.TrimSpace(window)
	if window == "" {
		return true, nil
	}
	parts := strings.Split(window, "-")
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid maintenance window %q", window)
	}
	parse := func(s string) (int, error) {
		t, e := time.Parse("15:04", strings.TrimSpace(s))
		if e != nil {
			return 0, e
		}
		return t.Hour()*60 + t.Minute(), nil
	}
	a, e := parse(parts[0])
	if e != nil {
		return false, fmt.Errorf("invalid maintenance window %q", window)
	}
	b, e := parse(parts[1])
	if e != nil {
		return false, fmt.Errorf("invalid maintenance window %q", window)
	}
	cur := at.Hour()*60 + at.Minute()
	if a == b {
		return true, nil
	}
	if a < b {
		return cur >= a && cur < b, nil
	}
	return cur >= a || cur < b, nil
}
