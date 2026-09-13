package propagation

import (
	"fmt"
	"sort"
	"time"
)

type ReconcileMode string

const (
	ReconcileNotify    ReconcileMode = "notify-only"
	ReconcileApproval  ReconcileMode = "approval-required"
	ReconcileAutomatic ReconcileMode = "automatic"
)

type Profile struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Selector          Selector      `json:"selector"`
	Kinds             []string      `json:"kinds"`
	Mode              ReconcileMode `json:"mode"`
	Schedule          string        `json:"schedule,omitempty"`
	MaintenanceWindow string        `json:"maintenance_window,omitempty"`
	Enabled           bool          `json:"enabled"`
	LastRunAt         *time.Time    `json:"last_run_at,omitempty"`
}
type Drift struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	ExpectedDigest string `json:"expected_digest"`
	ActualDigest   string `json:"actual_digest"`
	Node           string `json:"node"`
}
type ReconcileDecision struct {
	ProfileID     string    `json:"profile_id"`
	Drift         []Drift   `json:"drift"`
	Action        string    `json:"action"`
	NeedsApproval bool      `json:"needs_approval"`
	At            time.Time `json:"at"`
}

func (p Profile) Validate() error {
	if p.ID == "" || p.Name == "" {
		return fmt.Errorf("profile id and name required")
	}
	switch p.Mode {
	case ReconcileNotify, ReconcileApproval, ReconcileAutomatic:
	default:
		return fmt.Errorf("unsupported reconcile mode %q", p.Mode)
	}
	if len(p.Kinds) == 0 {
		return fmt.Errorf("at least one kind required")
	}
	if p.Schedule != "" {
		d, err := time.ParseDuration(p.Schedule)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid propagation schedule %q", p.Schedule)
		}
	}
	if _, err := InMaintenanceWindow(p.MaintenanceWindow, time.Now()); err != nil {
		return err
	}
	return nil
}
func DetectDrift(node string, expected []Envelope, actual map[string]Existing) []Drift {
	out := []Drift{}
	for _, e := range expected {
		k := e.Kind + "/" + e.ID
		a, ok := actual[k]
		if !ok || a.Digest != e.Digest {
			out = append(out, Drift{Kind: e.Kind, ID: e.ID, ExpectedDigest: e.Digest, ActualDigest: a.Digest, Node: node})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].ID < out[j].ID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
func DecideReconcile(p Profile, drift []Drift, at time.Time) ReconcileDecision {
	d := ReconcileDecision{ProfileID: p.ID, Drift: drift, At: at.UTC(), Action: "none"}
	if len(drift) == 0 || !p.Enabled {
		return d
	}
	switch p.Mode {
	case ReconcileNotify:
		d.Action = "notify"
	case ReconcileApproval:
		d.Action = "plan"
		d.NeedsApproval = true
	case ReconcileAutomatic:
		d.Action = "apply"
	}
	return d
}
