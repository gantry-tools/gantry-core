package propagation

import (
	"context"
	"fmt"
	"time"
)

type ProfileRunResult struct {
	ProfileID string `json:"profile_id"`
	Action    string `json:"action"`
	Drift     int    `json:"drift"`
	Applied   int    `json:"applied"`
	Failed    int    `json:"failed"`
	Error     string `json:"error,omitempty"`
}
type ProfileRunFunc func(context.Context, Profile) (ProfileRunResult, error)

// RunDueProfiles evaluates persisted profiles once. The caller owns cadence and
// provides product-specific cluster execution through fn.
func (m *Manager) RunDueProfiles(ctx context.Context, fn ProfileRunFunc) ([]ProfileRunResult, error) {
	if fn == nil {
		return nil, fmt.Errorf("profile runner required")
	}
	profiles, err := m.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	now := m.now()
	out := []ProfileRunResult{}
	for _, p := range profiles {
		due, e := Due(p, now)
		if e != nil {
			out = append(out, ProfileRunResult{ProfileID: p.ID, Error: e.Error()})
			continue
		}
		if !due {
			continue
		}
		r, e := fn(ctx, p)
		if e != nil {
			r.ProfileID = p.ID
			r.Error = e.Error()
		}
		p.LastRunAt = &now
		if se := m.SaveProfile(ctx, p); se != nil && e == nil {
			e = se
			r.Error = se.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

func ptrTime(t time.Time) *time.Time { return &t }
